package watcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBadgeRewardMatching(t *testing.T) {
	for _, tc := range []struct {
		reward, game, badge string
		want                bool
	}{
		{"Two Point Pickle Chat Badge", "Two Point Museum", "Two Point Pickle", true},
		{"Pickle", "Two Point Museum", "Two Point Pickle", true},
		{"Solasta 2 Multiplayer", "Solasta II", "Solasta II Multiplayer", true},
		{"Pet", "Path of Exile 2", "Unrelated Pet", false},
	} {
		if got := isBadgeReward(tc.reward, tc.game, tc.badge); got != tc.want {
			t.Errorf("match(%q,%q,%q)=%v", tc.reward, tc.game, tc.badge, got)
		}
	}
}

func TestSlugifyPreservesRomanNumerals(t *testing.T) {
	if got := slugify("Solasta II"); got != "solasta-ii" {
		t.Fatalf("slugify(Solasta II)=%q", got)
	}
}

func TestLoadBadgeCampaignsAcceptsSingleChannel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/drops", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"games":[{"game":"WARDOGS","campaigns":[{"id":"c1","name":"Launch","ends_at":"2027-01-01T00:00:00Z","all_channels":false,"channels":"wardogs","drops":[{"name":"WARDOG","requirement":"Watch 30m"}]}]}]}`))
	})
	mux.HandleFunc("/badges", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sets":[{"versions":[{"title":"WARDOG"}]}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	got, err := loadBadgeCampaigns(context.Background(), srv.Client(), srv.URL+"/drops", srv.URL+"/badges", time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Channels) != 1 || got[0].Channels[0] != "wardogs" {
		t.Fatalf("unexpected campaigns: %#v", got)
	}
}

func TestLoadBadgeCampaignsUsesCanonicalSourceSlug(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/drops", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"games":[{"source":"https://twitchdrops.app/game/dawnwalker","game":"The Blood of Dawnwalker","campaigns":[{"id":"c1","name":"Dawnwalker Launch","ends_at":"2027-01-01T00:00:00Z","all_channels":true,"drops":[{"name":"Dawnwalker Launch","requirement":"Watch 1h"}]}]}]}`))
	})
	mux.HandleFunc("/badges", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sets":[{"versions":[{"title":"The Blood of Dawnwalker Launch"}]}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := loadBadgeCampaigns(context.Background(), srv.Client(), srv.URL+"/drops", srv.URL+"/badges", time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].GameSlug != "dawnwalker" {
		t.Fatalf("unexpected campaigns: %#v", got)
	}
}

func TestLoadBadgeCampaignsHandlesFirstPartnersWatchBadgeAndDuplicates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/drops", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"games":[{"source":"https://twitchdrops.app/game/special-events","game":"Special Events","campaigns":[{"id":"later","name":"First Partners Collection","ends_at":"2027-02-01T00:00:00Z","all_channels":true,"drops":[{"name":"Great Ball","requirement":"Watch 20m"}]},{"id":"sooner","name":"First Partners Collection","ends_at":"2027-01-01T00:00:00Z","all_channels":true,"drops":[{"name":"Great Ball","requirement":"Watch 20m"}]}]}]}`))
	})
	mux.HandleFunc("/badges", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sets":[{"versions":[{"title":"Pichu","description":"This badge was earned during the Pokémon First Partners Collection campaign."}]},{"versions":[{"title":"Bulbasaur","description":"This badge was earned during the Pokémon First Partners Collection campaign."}]}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := loadBadgeCampaigns(context.Background(), srv.Client(), srv.URL+"/drops", srv.URL+"/badges", time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "sooner" || len(got[0].BadgeNames) != 1 || got[0].BadgeNames[0] != "Pichu" {
		t.Fatalf("unexpected campaigns: %#v", got)
	}
}

func TestLoadBadgeCampaignsMatchesCampaignDescription(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/drops", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"games":[{"source":"https://twitchdrops.app/game/special-events","game":"Special Events","campaigns":[{"id":"c1","name":"Community Celebration","ends_at":"2027-01-01T00:00:00Z","all_channels":true,"drops":[{"name":"Mystery Reward","requirement":"Watch 15m"}]}]}]}`))
	})
	mux.HandleFunc("/badges", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sets":[{"versions":[{"title":"Celebration Badge","description":"Earned during the Community Celebration campaign."}]}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := loadBadgeCampaigns(context.Background(), srv.Client(), srv.URL+"/drops", srv.URL+"/badges", time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].BadgeNames) != 1 || got[0].BadgeNames[0] != "Celebration Badge" {
		t.Fatalf("unexpected campaigns: %#v", got)
	}
}

func TestLoadBadgeCampaignsDoesNotConfusePaidBadgeWithWatchCampaign(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/drops", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"games":[{"source":"https://twitchdrops.app/game/grand-theft-auto-v","game":"Grand Theft Auto V","campaigns":[{"id":"watch","name":"nopixel V","ends_at":"2027-01-01T00:00:00Z","all_channels":true,"drops":[{"name":"GTA$250K","requirement":"Watch 1h"}]}]}]}`))
	})
	mux.HandleFunc("/badges", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sets":[{"versions":[{"title":"nopixel V Launch","description":"This badge was earned by subscribing during the nopixel V Launch."}]}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := loadBadgeCampaigns(context.Background(), srv.Client(), srv.URL+"/drops", srv.URL+"/badges", time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("paid badge matched watch campaign: %#v", got)
	}
}

func TestCompletedCampaigns(t *testing.T) {
	raw := json.RawMessage(`{"completedRewardCampaigns":[{"campaign":{"name":"Launch Badge","endAt":"2026-09-12T00:00:00Z","game":{"displayName":"Some Game"}}}]}`)
	got, err := completedCampaigns(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].GameSlug != "some-game" {
		t.Fatalf("unexpected signatures: %#v", got)
	}
	c := badgeCampaign{Name: "Launch Badge", GameSlug: "some-game", EndsAt: time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)}
	if !campaignCompleted(c, got) {
		t.Fatal("expected campaign to be completed")
	}
}

func TestCompletedCampaignsRejectsMalformedInventory(t *testing.T) {
	if _, err := completedCampaigns(json.RawMessage(`{`)); err == nil {
		t.Fatal("expected malformed inventory error")
	}
}

func TestCampaignCompletedUsesIDOrEndTimeTolerance(t *testing.T) {
	endsAt := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	campaign := badgeCampaign{ID: "campaign-id", Name: "Launch", GameSlug: "game", EndsAt: endsAt}
	if !campaignCompleted(campaign, []completedCampaignSignature{{ID: "campaign-id"}}) {
		t.Fatal("expected campaign ID match")
	}
	completed := completedCampaignSignature{GameSlug: "game", Name: "launch", EndsAt: endsAt.Add(4 * time.Second)}
	if !campaignCompleted(campaign, []completedCampaignSignature{completed}) {
		t.Fatal("expected completion within five-second tolerance")
	}
	completed.EndsAt = endsAt.Add(6 * time.Second)
	if campaignCompleted(campaign, []completedCampaignSignature{completed}) {
		t.Fatal("unexpected completion outside tolerance")
	}
}
