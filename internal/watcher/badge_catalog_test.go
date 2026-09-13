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
