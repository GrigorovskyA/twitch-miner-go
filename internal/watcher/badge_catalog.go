package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	defaultDropsCatalogURL  = "https://gist.githubusercontent.com/zarmstrong/72433778ae596815f4c6ff5e1d278cd2/raw/twitch-drops.json"
	defaultBadgesCatalogURL = "https://gist.githubusercontent.com/zarmstrong/d4fc5f87e2a5258a28421f7fdb8037d6/raw/twitch-badges.json"
	maxCatalogBytes         = 4 << 20
)

type badgeCampaign struct {
	ID, Name, GameName, GameSlug string
	StartsAt, EndsAt             time.Time
	AllChannels                  bool
	Channels                     []string
	BadgeNames                   []string
}

type flexibleStrings []string

func (s *flexibleStrings) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*s = nil
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err == nil {
		*s = many
		return nil
	}
	var one string
	if err := json.Unmarshal(data, &one); err != nil {
		return err
	}
	if strings.TrimSpace(one) == "" {
		*s = nil
	} else {
		*s = []string{one}
	}
	return nil
}

type dropsCatalog struct {
	Games []struct {
		Game      string `json:"game"`
		Campaigns []struct {
			ID          string          `json:"id"`
			Name        string          `json:"name"`
			StartsAt    *time.Time      `json:"starts_at"`
			EndsAt      *time.Time      `json:"ends_at"`
			AllChannels bool            `json:"all_channels"`
			Channels    flexibleStrings `json:"channels"`
			Drops       []struct {
				Name        string `json:"name"`
				Requirement string `json:"requirement"`
			} `json:"drops"`
		} `json:"campaigns"`
	} `json:"games"`
}

type badgesCatalog struct {
	Sets []struct {
		Versions []struct {
			Title string `json:"title"`
		} `json:"versions"`
	} `json:"sets"`
}

var badgeWordsRE = regexp.MustCompile(`[a-z0-9]+`)

var romanBadgeNumbers = map[string]string{
	"i": "1", "ii": "2", "iii": "3", "iv": "4", "v": "5",
	"vi": "6", "vii": "7", "viii": "8", "ix": "9", "x": "10",
}

func words(s string) []string { return badgeWordsRE.FindAllString(strings.ToLower(s), -1) }

func normalizedBadgeWords(s string) []string {
	result := words(s)
	for i, word := range result {
		if number, ok := romanBadgeNumbers[word]; ok {
			result[i] = number
		}
	}
	return result
}
func comparableWords(s string) []string {
	w := normalizedBadgeWords(s)
	if len(w) >= 2 && w[len(w)-2] == "chat" && w[len(w)-1] == "badge" {
		w = w[:len(w)-2]
	} else if len(w) > 0 && w[len(w)-1] == "badge" {
		w = w[:len(w)-1]
	}
	return w
}
func equalWords(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isBadgeReward(reward, game, badge string) bool {
	rw := comparableWords(reward)
	bw := comparableWords(badge)
	if len(rw) == 0 || len(bw) == 0 {
		return false
	}
	if equalWords(rw, bw) {
		return true
	}
	gw := make(map[string]bool)
	for _, w := range normalizedBadgeWords(game) {
		gw[w] = true
	}
	if len(bw) > len(rw) && equalWords(bw[len(bw)-len(rw):], rw) {
		for _, p := range bw[:len(bw)-len(rw)] {
			if gw[p] {
				return true
			}
		}
	}
	if len(rw) > len(bw) && equalWords(rw[len(rw)-len(bw):], bw) {
		for _, p := range rw[:len(rw)-len(bw)] {
			if gw[p] {
				return true
			}
		}
	}
	return false
}

func slugify(s string) string { return strings.Join(words(s), "-") }

func fetchJSON(ctx context.Context, client *http.Client, url string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("catalog returned HTTP %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, maxCatalogBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(data) > maxCatalogBytes {
		return fmt.Errorf("catalog exceeds %d bytes", maxCatalogBytes)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("invalid catalog JSON: %w", err)
	}
	return nil
}

func loadBadgeCampaigns(ctx context.Context, client *http.Client, dropsURL, badgesURL string, now time.Time) ([]badgeCampaign, error) {
	if dropsURL == "" {
		dropsURL = defaultDropsCatalogURL
	}
	if badgesURL == "" {
		badgesURL = defaultBadgesCatalogURL
	}
	var dc dropsCatalog
	if err := fetchJSON(ctx, client, dropsURL, &dc); err != nil {
		return nil, fmt.Errorf("drops catalog: %w", err)
	}
	var bc badgesCatalog
	if err := fetchJSON(ctx, client, badgesURL, &bc); err != nil {
		return nil, fmt.Errorf("badges catalog: %w", err)
	}
	var titles []string
	for _, set := range bc.Sets {
		for _, v := range set.Versions {
			if strings.TrimSpace(v.Title) != "" {
				titles = append(titles, v.Title)
			}
		}
	}
	var result []badgeCampaign
	for _, game := range dc.Games {
		for _, campaign := range game.Campaigns {
			if campaign.StartsAt != nil && campaign.StartsAt.After(now) {
				continue
			}
			if campaign.EndsAt == nil || !campaign.EndsAt.After(now) {
				continue
			}
			var matches []string
			for _, drop := range campaign.Drops {
				if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(drop.Requirement)), "watch ") {
					continue
				}
				for _, title := range titles {
					if isBadgeReward(drop.Name, game.Game, title) {
						matches = append(matches, drop.Name)
						break
					}
				}
			}
			if len(matches) == 0 {
				continue
			}
			entry := badgeCampaign{ID: campaign.ID, Name: campaign.Name, GameName: game.Game, GameSlug: slugify(game.Game), EndsAt: *campaign.EndsAt, AllChannels: campaign.AllChannels, Channels: campaign.Channels, BadgeNames: matches}
			if campaign.StartsAt != nil {
				entry.StartsAt = *campaign.StartsAt
			}
			result = append(result, entry)
		}
	}
	return result, nil
}

func ownsCampaignBadge(c badgeCampaign, owned map[string]struct{}) bool {
	for _, reward := range c.BadgeNames {
		for title := range owned {
			if isBadgeReward(reward, c.GameName, title) {
				return true
			}
		}
	}
	return false
}

type completedCampaignSignature struct {
	ID, GameSlug, Name string
	EndsAt             time.Time
}

func completedCampaigns(raw json.RawMessage) ([]completedCampaignSignature, error) {
	var inv struct {
		Completed []struct {
			Campaign *struct {
				ID     string    `json:"id"`
				Name   string    `json:"name"`
				EndAt  time.Time `json:"endAt"`
				EndsAt time.Time `json:"endsAt"`
				Game   struct {
					Name        string `json:"name"`
					DisplayName string `json:"displayName"`
				} `json:"game"`
			} `json:"campaign"`
		} `json:"completedRewardCampaigns"`
	}
	if err := json.Unmarshal(raw, &inv); err != nil {
		return nil, fmt.Errorf("parse completed reward campaigns: %w", err)
	}
	var result []completedCampaignSignature
	for _, item := range inv.Completed {
		if item.Campaign == nil {
			continue
		}
		end := item.Campaign.EndAt
		if end.IsZero() {
			end = item.Campaign.EndsAt
		}
		game := item.Campaign.Game.DisplayName
		if game == "" {
			game = item.Campaign.Game.Name
		}
		if game != "" && item.Campaign.Name != "" && !end.IsZero() {
			result = append(result, completedCampaignSignature{item.Campaign.ID, slugify(game), strings.ToLower(strings.TrimSpace(item.Campaign.Name)), end})
		}
	}
	return result, nil
}

func campaignCompleted(c badgeCampaign, completed []completedCampaignSignature) bool {
	for _, s := range completed {
		if c.ID != "" && s.ID != "" && c.ID == s.ID {
			return true
		}
		const endTimeTolerance = 5 * time.Second
		if c.GameSlug == s.GameSlug && strings.EqualFold(c.Name, s.Name) && c.EndsAt.Sub(s.EndsAt) < endTimeTolerance && s.EndsAt.Sub(c.EndsAt) < endTimeTolerance {
			return true
		}
	}
	return false
}
