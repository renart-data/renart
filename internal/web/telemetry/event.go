// Package telemetry owns Renart's bounded, opt-out product usage contract.
// Observations cannot contain user-authored strings or arbitrary properties.
package telemetry

import (
	"regexp"
	"runtime"
	"time"

	"github.com/google/uuid"
)

// renart:web-name UsageEventName
type Name string

// renart:web-name UsageOutcome
type Outcome string

// renart:web-name UsageTrigger
type Trigger string

// renart:web-name UsageSurface
type Surface string

// renart:web-name UsageMode
type Mode string

const (
	SessionStarted       Name    = "workspace_session_started"
	PipelineFinished     Name    = "pipeline_run_finished"
	NotebookFinished     Name    = "notebook_run_finished"
	PresentationFinished Name    = "presentation_run_finished"
	Success              Outcome = "success"
	Failed               Outcome = "failed"
	Cancelled            Outcome = "cancelled"
	Manual               Trigger = "manual"
	Scheduled            Trigger = "scheduled"
	Automatic            Trigger = "automatic"
	CLI                  Trigger = "cli"
	API                  Trigger = "api"
	Terminal             Surface = "cli"
	Web                  Surface = "web"
	Pipeline             Surface = "pipeline"
	Asset                Surface = "asset"
	Notebook             Surface = "notebook"
	Dashboard            Surface = "dashboard"
	Report               Surface = "report"
	Unlinked             Mode    = "unlinked"
	Installation         Mode    = "installation"
)

// Observation is an internal, closed allowlist. Invalid combinations are dropped.
// Duration and item counts become coarse buckets before entering the queue.
type Observation struct {
	Name     Name
	Outcome  Outcome
	Trigger  Trigger
	Surface  Surface
	Duration time.Duration
	Items    int
}

// Event is the entire outbound contract. Never add paths, names, SQL, errors,
// connection details, project IDs or a free-form properties map here.
// renart:web
// renart:web-name UsageEvent
type Event struct {
	SchemaVersion   int     `json:"schema_version"`
	EventID         string  `json:"event_id"`
	Hour            string  `json:"hour"`
	Version         string  `json:"version"`
	OS              string  `json:"os"`
	Architecture    string  `json:"architecture"`
	InstallationID  string  `json:"installation_id,omitempty"`
	Name            Name    `json:"name"`
	Outcome         Outcome `json:"outcome,omitempty"`
	Trigger         Trigger `json:"trigger,omitempty"`
	Surface         Surface `json:"surface"`
	DurationBucket  string  `json:"duration_bucket,omitempty"`
	ItemCountBucket string  `json:"item_count_bucket,omitempty"`
}

type Batch struct {
	SchemaVersion int     `json:"schema_version"`
	Events        []Event `json:"events"`
}

var releaseVersion = regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:-(?:alpha|beta|rc)[.\d]*)?$`)

func (o Observation) valid() bool {
	if o.Name == SessionStarted {
		return (o.Surface == Web || o.Surface == Terminal) && o.Outcome == "" && o.Trigger == ""
	}
	if o.Outcome != Success && o.Outcome != Failed && o.Outcome != Cancelled {
		return false
	}
	switch o.Trigger {
	case Manual, Scheduled, Automatic, CLI, API:
	default:
		return false
	}
	switch o.Name {
	case PipelineFinished:
		return o.Surface == Pipeline || o.Surface == Asset
	case NotebookFinished:
		return o.Surface == Notebook
	case PresentationFinished:
		return o.Surface == Dashboard || o.Surface == Report
	default:
		return false
	}
}

func newEvent(o Observation, version, installationID string, now time.Time) Event {
	if !releaseVersion.MatchString(version) {
		version = "dev"
	}
	event := Event{SchemaVersion: 1, EventID: uuid.NewString(), Hour: now.UTC().Truncate(time.Hour).Format(time.RFC3339), Version: version, OS: runtime.GOOS, Architecture: runtime.GOARCH, InstallationID: installationID, Name: o.Name, Outcome: o.Outcome, Trigger: o.Trigger, Surface: o.Surface}
	if o.Name != SessionStarted {
		event.DurationBucket = durationBucket(o.Duration)
		if o.Items >= 0 {
			event.ItemCountBucket = countBucket(o.Items)
		}
	}
	return event
}

func durationBucket(d time.Duration) string {
	switch {
	case d < time.Second:
		return "under_1s"
	case d < 10*time.Second:
		return "1s_to_10s"
	case d < time.Minute:
		return "10s_to_1m"
	case d < 10*time.Minute:
		return "1m_to_10m"
	default:
		return "10m_or_more"
	}
}
func countBucket(n int) string {
	switch {
	case n <= 0:
		return "0"
	case n == 1:
		return "1"
	case n < 10:
		return "2_to_9"
	case n < 100:
		return "10_to_99"
	default:
		return "100_or_more"
	}
}
