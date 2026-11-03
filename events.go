package main

import (
	"encoding/json"
	"io"
	"sync"
)

// Events writes machine-readable progress for --json as JSON lines: one
// object per line, each with an "event" field. A nil *Events discards
// everything, so callers never need to check.
//
// The event names and fields are a contract with the macOS app in
// ../UnpackDropUI; keep them backward compatible.
type Events struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// NewEvents returns an event writer on w.
func NewEvents(w io.Writer) *Events {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &Events{enc: enc}
}

func (e *Events) emit(v any) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.enc.Encode(v) // best effort, like terminal output
}

// startEvent opens the events of one source archive.
type startEvent struct {
	Event  string `json:"event"` // "start"
	Source string `json:"source"`
}

// releaseEvent names the release once the outer archive is extracted.
type releaseEvent struct {
	Event   string `json:"event"` // "release"
	Release string `json:"release"`
}

// stepEvent starts step Step of Steps. Total is in bytes, 0 if unknown.
type stepEvent struct {
	Event string `json:"event"` // "step"
	Step  int    `json:"step"`
	Steps int    `json:"steps"`
	Label string `json:"label"`
	Total int64  `json:"total"`
}

// progressEvent reports bytes processed in the current step.
type progressEvent struct {
	Event   string `json:"event"` // "progress"
	Step    int    `json:"step"`
	Current int64  `json:"current"`
	Total   int64  `json:"total"`
}

// stepDoneEvent closes a step that succeeded.
type stepDoneEvent struct {
	Event string `json:"event"` // "step_done"
	Step  int    `json:"step"`
}

// warningEvent reports a condition that does not stop the run.
type warningEvent struct {
	Event   string `json:"event"` // "warning"
	Message string `json:"message"`
}

// doneEvent closes a successful run, or the plan of a dry run.
type doneEvent struct {
	Event        string   `json:"event"` // "done"
	Source       string   `json:"source"`
	Release      string   `json:"release"`
	Folder       string   `json:"folder,omitempty"`         // unpacked folder, unless repacked and not kept
	Archive      string   `json:"archive,omitempty"`        // repacked archive
	ArchiveSize  int64    `json:"archive_size,omitempty"`   // 0 in a dry run
	Files        int      `json:"files"`                    // number of kept files
	Size         int64    `json:"size"`                     // their total size in bytes
	Info         []string `json:"info"`                     // .nfo/.diz files at the root
	SourceDelete bool     `json:"source_deleted,omitempty"` // the source archive was (or would be) removed
	DryRun       bool     `json:"dry_run,omitempty"`
	Problems     []string `json:"problems,omitempty"` // dry run: reasons the real run would fail
}

// errorEvent closes a failed run. Source is empty for errors that stop
// everything before any archive is opened, such as a missing tool.
type errorEvent struct {
	Event       string `json:"event"` // "error"
	Source      string `json:"source,omitempty"`
	Message     string `json:"message"`
	Interrupted bool   `json:"interrupted,omitempty"`
}

func newDoneEvent(source string, r *Result) doneEvent {
	info := r.Meta()
	if info == nil {
		info = []string{}
	}
	return doneEvent{
		Event:        "done",
		Source:       source,
		Release:      r.Release,
		Folder:       r.Dest,
		Archive:      r.Archive,
		ArchiveSize:  r.ArchiveSize,
		Files:        len(r.Files),
		Size:         r.Size,
		Info:         info,
		SourceDelete: r.DeletesSource || r.ReplacesSource,
		DryRun:       r.DryRun,
		Problems:     r.Problems,
	}
}
