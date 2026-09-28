package alerts

import (
	"fmt"
	"math"

	"github.com/verrloren/radaro/internal/store"
)

// ThresholdEvent is an active volume or sentiment crossing.
type ThresholdEvent struct {
	Type    string
	Text    string
	Payload map[string]any
}

// ThresholdSettings configure the rolling-window checks. A zero multiplier or
// drop disables that check.
type ThresholdSettings struct {
	WindowHours      int
	MinimumMentions  int
	VolumeMultiplier float64
	SentimentDrop    float64
}

// EventTypes are evaluated on every scan; a nil event clears that episode.
var EventTypes = []string{"volume_spike", "sentiment_drop"}

// EvaluateThresholds returns the active event per type (nil = clear).
func EvaluateThresholds(query string, m store.AlertMetrics, s ThresholdSettings) map[string]*ThresholdEvent {
	events := map[string]*ThresholdEvent{"volume_spike": nil, "sentiment_drop": nil}

	if s.VolumeMultiplier > 0 && m.CurrentCount >= s.MinimumMentions && m.BaselineCount > 0 {
		var ratio *float64
		if m.BaselineAverage > 0 {
			r := float64(m.CurrentCount) / m.BaselineAverage
			ratio = &r
		}
		if ratio == nil || *ratio >= s.VolumeMultiplier {
			comparison := "up from a zero baseline"
			var multiplier any
			if ratio != nil {
				comparison = fmt.Sprintf("%.1f× the baseline", *ratio)
				multiplier = round3(*ratio)
			}
			events["volume_spike"] = &ThresholdEvent{
				Type: "volume_spike",
				Text: fmt.Sprintf("Radaro alert: volume spike for “%s”\n%d mentions in the last %dh, %s (%.1f average).",
					query, m.CurrentCount, s.WindowHours, comparison, m.BaselineAverage),
				Payload: map[string]any{
					"event": "radaro.volume_spike", "query": query, "window_hours": s.WindowHours,
					"current_count": m.CurrentCount, "baseline_average": round3(m.BaselineAverage),
					"multiplier": multiplier, "threshold": s.VolumeMultiplier,
				},
			}
		}
	}

	if s.SentimentDrop > 0 && m.CurrentCount >= s.MinimumMentions && m.BaselineCount >= s.MinimumMentions &&
		m.CurrentNetSentiment != nil && m.BaselineNetSentiment != nil {
		cur, base := *m.CurrentNetSentiment, *m.BaselineNetSentiment
		drop := base - cur
		if drop >= s.SentimentDrop {
			events["sentiment_drop"] = &ThresholdEvent{
				Type: "sentiment_drop",
				Text: fmt.Sprintf("Radaro alert: sentiment deterioration for “%s”\nNet sentiment is %s in the last %dh, down %.0f%% from the %s baseline.",
					query, signedPercent(cur), s.WindowHours, drop*100, signedPercent(base)),
				Payload: map[string]any{
					"event": "radaro.sentiment_drop", "query": query, "window_hours": s.WindowHours,
					"current_count": m.CurrentCount, "current_net_sentiment": round3(cur),
					"baseline_count": m.BaselineCount, "baseline_net_sentiment": round3(base),
					"drop": round3(drop), "threshold": s.SentimentDrop,
				},
			}
		}
	}
	return events
}

func signedPercent(v float64) string { return fmt.Sprintf("%+.0f%%", v*100) }

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
