package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// decodeEvents parses JSON lines into generic objects.
func decodeEvents(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("not a JSON line: %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

func eventNames(events []map[string]any) []string {
	names := make([]string, len(events))
	for i, e := range events {
		names[i], _ = e["event"].(string)
	}
	return names
}

func TestStepBarEvents(t *testing.T) {
	var buf bytes.Buffer
	opt := Options{Events: NewEvents(&buf)}
	b := newStepBar(opt, 2, 3, "Unpacking 4 zip(s)", 100)
	b.Set(40)
	b.Set(60) // within eventInterval of the previous one: not reported
	b.Finish()
	b.Finish() // no second step_done

	events := decodeEvents(t, buf.Bytes())
	if got, want := eventNames(events), []string{"step", "progress", "progress", "step_done"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	step := events[0]
	if step["step"] != 2.0 || step["steps"] != 3.0 || step["label"] != "Unpacking 4 zip(s)" || step["total"] != 100.0 {
		t.Fatalf("step event = %v", step)
	}
	if events[1]["current"] != 40.0 || events[2]["current"] != 100.0 {
		t.Fatalf("progress events = %v, %v", events[1], events[2])
	}
}

func TestNilEventsDiscard(t *testing.T) {
	var ev *Events
	ev.emit(warningEvent{Event: "warning", Message: "ignored"}) // must not panic
	b := newStepBar(Options{}, 1, 1, "x", 10)
	b.Set(5)
	b.Finish()
}

// TestUnpackJSONEvents checks the event sequence of a full run with repack,
// on a release without an .nfo file so a warning is reported.
func TestUnpackJSONEvents(t *testing.T) {
	requireTools(t, "rar", "unzip")
	archive := buildRelease(t, t.TempDir(), []string{"file_id.diz"}, []string{"file_id.diz"})

	var buf bytes.Buffer
	res, err := Unpack(context.Background(), Options{
		Archive:   archive,
		OutputDir: t.TempDir(),
		Repack:    FormatZip,
		Level:     LevelFast,
		Events:    NewEvents(&buf),
	})
	if err != nil {
		t.Fatal(err)
	}

	var steps, done []float64
	var warnings []string
	release := ""
	for _, e := range decodeEvents(t, buf.Bytes()) {
		switch e["event"] {
		case "release":
			release, _ = e["release"].(string)
		case "step":
			steps = append(steps, e["step"].(float64))
			if e["steps"] != 5.0 {
				t.Fatalf("steps = %v, want 5", e["steps"])
			}
		case "step_done":
			done = append(done, e["step"].(float64))
		case "warning":
			warnings = append(warnings, e["message"].(string))
		}
	}
	if release != res.Release {
		t.Fatalf("release event = %q, want %q", release, res.Release)
	}
	want := []float64{1, 2, 3, 4, 5}
	if !reflect.DeepEqual(steps, want) || !reflect.DeepEqual(done, want) {
		t.Fatalf("steps = %v, done = %v, want %v", steps, done, want)
	}
	if len(warnings) != 1 || warnings[0] != "no .nfo file found in Some.App.v2.0-XYZ.rar" {
		t.Fatalf("warnings = %q", warnings)
	}

	d := newDoneEvent(archive, res)
	if d.Archive != res.Archive || d.Folder != "" || d.Files != len(res.Files) || d.ArchiveSize == 0 {
		t.Fatalf("done event = %+v", d)
	}
	if !reflect.DeepEqual(d.Info, []string{"file_id.diz"}) {
		t.Fatalf("info = %v", d.Info)
	}
}
