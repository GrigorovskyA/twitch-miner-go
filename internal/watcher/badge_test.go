package watcher

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Guliveer/twitch-miner-go/internal/config"
	"github.com/Guliveer/twitch-miner-go/internal/gql"
	"github.com/Guliveer/twitch-miner-go/internal/logger"
	"github.com/Guliveer/twitch-miner-go/internal/model"
)

type stubBadgeGQL struct {
	owned        map[string]struct{}
	streams      map[string][]gql.TopStream
	streamInfo   map[string]*gql.StreamInfoResponse
	channelIDs   map[string]string
	categoryHits []string
	raw          json.RawMessage
}

func (s *stubBadgeGQL) GetAvailableBadgeNames(context.Context) (map[string]struct{}, error) {
	return s.owned, nil
}

func (s *stubBadgeGQL) GetDropsInventory(context.Context) (json.RawMessage, error) {
	if s.raw == nil {
		return json.RawMessage(`{}`), nil
	}
	return s.raw, nil
}

func (s *stubBadgeGQL) GetTopStreamsByCategory(_ context.Context, slug string, _ int, _ bool) ([]gql.TopStream, error) {
	s.categoryHits = append(s.categoryHits, slug)
	return s.streams[slug], nil
}

func (s *stubBadgeGQL) GetStreamInfo(_ context.Context, login string) (*gql.StreamInfoResponse, error) {
	return s.streamInfo[login], nil
}

func (s *stubBadgeGQL) GetUserID(_ context.Context, login string) (string, error) {
	if id := s.channelIDs[login]; id != "" {
		return id, nil
	}
	return "id-" + login, nil
}

func (s *stubBadgeGQL) GetAvailableCampaigns(context.Context, string) ([]string, error) {
	return []string{"drop-campaign"}, nil
}

func testBadgeWatcher(t *testing.T, client *stubBadgeGQL, limit int, campaigns ...badgeCampaign) *BadgeWatcher {
	t.Helper()
	log, err := logger.Setup(logger.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	bw := NewBadgeWatcher(config.BadgeWatcherConfig{StreamerLimit: limit}, client, nil, log, nil, &model.StreamerSettings{})
	bw.campaigns = campaigns
	bw.catalogLoadedAt = time.Now()
	return bw
}

func activeBadgeCampaign(id, name, slug, badge string) badgeCampaign {
	return badgeCampaign{ID: id, Name: name, GameName: name, GameSlug: slug, EndsAt: time.Now().Add(time.Hour), AllChannels: true, BadgeNames: []string{badge}}
}

func TestBadgeWatcherEvaluateAddsAndLimitsStreams(t *testing.T) {
	campaigns := []badgeCampaign{
		activeBadgeCampaign("one", "Game One", "game-one", "Badge One"),
		activeBadgeCampaign("two", "Game Two", "game-two", "Badge Two"),
		activeBadgeCampaign("three", "Game Three", "game-three", "Badge Three"),
	}
	client := &stubBadgeGQL{streams: map[string][]gql.TopStream{
		"game-one":   {{Username: "one", ChannelID: "1", GameName: "Game One"}},
		"game-two":   {{Username: "two", ChannelID: "2", GameName: "Game Two"}},
		"game-three": {{Username: "three", ChannelID: "3", GameName: "Game Three"}},
	}}
	bw := testBadgeWatcher(t, client, 99, campaigns...)
	var added []*model.Streamer
	bw.evaluate(context.Background(), func(_ context.Context, streamer *model.Streamer) {
		added = append(added, streamer)
	}, func(string, string) {}, func() []*model.Streamer { return added })

	if len(added) != 2 {
		t.Fatalf("added %d streamers, want runtime cap of 2", len(added))
	}
	for _, streamer := range added {
		if !streamer.IsBadgeWatched || !streamer.Settings.ClaimDrops || !streamer.Settings.DropsOnly || streamer.Settings.Chat != model.ChatNever {
			t.Fatalf("unexpected badge streamer settings: %#v", streamer)
		}
	}
}

func TestBadgeWatcherEvaluateMarksExistingStreamer(t *testing.T) {
	campaign := activeBadgeCampaign("one", "Game One", "game-one", "Badge One")
	client := &stubBadgeGQL{streams: map[string][]gql.TopStream{
		"game-one": {{Username: "existing", ChannelID: "1", GameName: "Game One"}},
	}}
	existing := model.NewStreamer("existing")
	existing.IsOnline = true
	existing.Stream.Game = &model.GameInfo{Slug: "game-one", Name: "Game One"}
	bw := testBadgeWatcher(t, client, 1, campaign)
	added := false
	bw.evaluate(context.Background(), func(context.Context, *model.Streamer) { added = true }, func(string, string) {}, func() []*model.Streamer { return []*model.Streamer{existing} })

	if added || !existing.IsBadgeWatched || existing.BadgeCampaign != campaign.Name {
		t.Fatalf("existing streamer was not marked correctly: added=%v streamer=%#v", added, existing)
	}
}

func TestBadgeWatcherFindsRestrictedSpecialEventChannelOutsideCampaignCategory(t *testing.T) {
	campaign := badgeCampaign{
		ID:         "mouseathon",
		Name:       "Ironmouse Subathon 2026",
		GameName:   "Special Events",
		GameSlug:   "special-events",
		EndsAt:     time.Now().Add(time.Hour),
		Channels:   []string{"ironmouse"},
		BadgeNames: []string{"Mouseathon"},
	}
	client := &stubBadgeGQL{
		streams: map[string][]gql.TopStream{},
		streamInfo: map[string]*gql.StreamInfoResponse{
			"ironmouse": {Game: &model.GameInfo{ID: "509658", Slug: "just-chatting", Name: "Just Chatting"}, ViewersCount: 12000},
		},
		channelIDs: map[string]string{"ironmouse": "123456"},
	}
	bw := testBadgeWatcher(t, client, 1, campaign)
	var added []*model.Streamer
	get := func() []*model.Streamer { return added }
	bw.evaluate(context.Background(), func(_ context.Context, streamer *model.Streamer) {
		added = append(added, streamer)
	}, func(string, string) {}, get)

	if len(added) != 1 || added[0].Username != "ironmouse" {
		t.Fatalf("restricted special-event streamer not added: %#v", added)
	}
	if got := added[0].Stream.Game.Slug; got != "just-chatting" {
		t.Fatalf("stream category=%q, want actual category just-chatting", got)
	}
	if len(client.categoryHits) != 0 {
		t.Fatalf("restricted campaign unexpectedly searched categories: %v", client.categoryHits)
	}

	bw.evaluate(context.Background(), func(context.Context, *model.Streamer) {}, func(_, reason string) {
		t.Fatalf("restricted special-event streamer retired on next poll: %s", reason)
	}, get)
}

func TestBadgeWatcherRestrictedCampaignRequiresMatchingCategoryOutsideSpecialEvents(t *testing.T) {
	campaign := badgeCampaign{
		ID:         "restricted",
		Name:       "Restricted Game Campaign",
		GameName:   "Expected Game",
		GameSlug:   "expected-game",
		EndsAt:     time.Now().Add(time.Hour),
		Channels:   []string{"allowed"},
		BadgeNames: []string{"Expected Badge"},
	}
	client := &stubBadgeGQL{
		streams: map[string][]gql.TopStream{},
		streamInfo: map[string]*gql.StreamInfoResponse{
			"allowed": {Game: &model.GameInfo{Slug: "other-game", Name: "Other Game"}},
		},
	}
	bw := testBadgeWatcher(t, client, 1, campaign)
	var added []*model.Streamer
	bw.evaluate(context.Background(), func(_ context.Context, streamer *model.Streamer) {
		added = append(added, streamer)
	}, func(string, string) {}, func() []*model.Streamer { return added })

	if len(added) != 0 {
		t.Fatalf("restricted streamer in wrong category was added: %#v", added)
	}
}

func TestBadgeWatcherRetiresOwnedCampaignAndCleansUp(t *testing.T) {
	campaign := activeBadgeCampaign("one", "Game One", "game-one", "Badge One")
	client := &stubBadgeGQL{owned: map[string]struct{}{"badge one": {}}, streams: map[string][]gql.TopStream{}}
	streamer := model.NewStreamer("added")
	streamer.IsOnline = true
	streamer.Stream.Game = &model.GameInfo{Slug: "game-one"}
	bw := testBadgeWatcher(t, client, 1, campaign)
	bw.tracked[campaign.ID] = badgeTracked{username: streamer.Username, added: true}
	var removals []string
	remove := func(_ string, reason string) { removals = append(removals, reason) }
	bw.evaluate(context.Background(), func(context.Context, *model.Streamer) {}, remove, func() []*model.Streamer { return []*model.Streamer{streamer} })
	if len(removals) != 1 || removals[0] != "badge_campaign_completed_or_expired" {
		t.Fatalf("unexpected retirement: %v", removals)
	}

	bw.tracked["cleanup"] = badgeTracked{username: streamer.Username, added: true}
	bw.cleanup(remove, func() []*model.Streamer { return []*model.Streamer{streamer} })
	if len(removals) != 2 || removals[1] != "badge_watcher_shutdown" || len(bw.tracked) != 0 {
		t.Fatalf("unexpected cleanup: removals=%v tracked=%v", removals, bw.tracked)
	}
}

func TestBadgeWatcherRetiresInvalidTrackedStreamer(t *testing.T) {
	for _, tc := range []struct {
		name     string
		online   bool
		category string
	}{
		{name: "offline", online: false, category: "game-one"},
		{name: "changed category", online: true, category: "other-game"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			campaign := activeBadgeCampaign("one", "Game One", "game-one", "Badge One")
			client := &stubBadgeGQL{streams: map[string][]gql.TopStream{}}
			streamer := model.NewStreamer("tracked")
			streamer.IsOnline = tc.online
			streamer.Stream.Game = &model.GameInfo{Slug: tc.category}
			bw := testBadgeWatcher(t, client, 1, campaign)
			bw.tracked[campaign.ID] = badgeTracked{username: streamer.Username, added: true}
			var reason string
			bw.evaluate(context.Background(), func(context.Context, *model.Streamer) {}, func(_ string, got string) { reason = got }, func() []*model.Streamer { return []*model.Streamer{streamer} })
			if reason != "badge_stream_no_longer_eligible" {
				t.Fatalf("removal reason=%q", reason)
			}
		})
	}
}
