package commands

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// captureServerMessages records the update progress messages sent instead
// of writing them to a WebSocket.
func captureServerMessages(t *testing.T) *[]updateProgressMessage {
	t.Helper()
	var mu sync.Mutex
	got := []updateProgressMessage{}
	old := sendServerMessage
	sendServerMessage = func(b []byte) error {
		var m updateProgressMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Errorf("message is not JSON: %v", err)
		}
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
		return nil
	}
	t.Cleanup(func() { sendServerMessage = old })
	return &got
}

func stages(msgs []updateProgressMessage) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Stage)
	}
	return out
}

func TestUpdateReporterFromServerReportsEveryStage(t *testing.T) {
	sent := captureServerMessages(t)
	u := newUpdateReporter(updateFromServer, "1.0.0")
	u.stage(updateStageChecking, "")
	u.setTarget("1.1.0")
	u.stage(updateStageDownloading, "")
	u.stage(updateStageRestarting, "")
	got := stages(*sent)
	want := []string{updateStageChecking, updateStageDownloading, updateStageRestarting}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stages = %v, want %v", got, want)
	}
	last := (*sent)[2]
	if last.Type != updateProgressType || last.FromVersion != "1.0.0" || last.ToVersion != "1.1.0" {
		t.Fatalf("last message = %+v", last)
	}
	if (*sent)[0].ToVersion != "" {
		t.Fatalf("target reported before it was known: %+v", (*sent)[0])
	}
	for _, m := range *sent {
		if m.Message != "" {
			t.Fatalf("stage %q sent the message %q, want none", m.Stage, m.Message)
		}
	}
}

// The check after every report stays quiet until an update really starts,
// then reports like any other.
func TestUpdateReporterAutomaticStaysQuietUntilDownload(t *testing.T) {
	sent := captureServerMessages(t)
	u := newUpdateReporter(updateAutomatic, "1.0.0")
	u.stage(updateStageChecking, "")
	u.fail(updateSkippedError{errors.New("updated less than 5 minutes ago")})
	u.stage(updateStageUpToDate, "")
	if len(*sent) != 0 {
		t.Fatalf("automatic check reported %v before downloading", stages(*sent))
	}
	u.stage(updateStageDownloading, "")
	u.fail(errors.New("binary hash mismatch"))
	got := stages(*sent)
	if strings.Join(got, ",") != updateStageDownloading+","+updateStageFailed {
		t.Fatalf("stages = %v", got)
	}
	if (*sent)[1].Message != "binary hash mismatch" {
		t.Fatalf("failure message = %q", (*sent)[1].Message)
	}
}

func TestUpdateReporterManualSendsNothing(t *testing.T) {
	sent := captureServerMessages(t)
	u := newUpdateReporter(updateManual, "1.0.0")
	u.stage(updateStageDownloading, "")
	u.percent(50)
	u.fail(errors.New("boom"))
	if len(*sent) != 0 {
		t.Fatalf("manual update reported %v", stages(*sent))
	}
}

func TestUpdateReporterSkipAndFailure(t *testing.T) {
	sent := captureServerMessages(t)
	u := newUpdateReporter(updateFromServer, "1.0.0")
	u.fail(updateSkippedError{errors.New("a patch run is in progress")})
	u.fail(errors.New("failed to replace executable:\npermission denied"))
	u.fail(nil)
	if len(*sent) != 2 {
		t.Fatalf("sent %v, want skipped then failed", stages(*sent))
	}
	if m := (*sent)[0]; m.Stage != updateStageSkipped || m.Message != "a patch run is in progress" {
		t.Fatalf("skip = %+v", m)
	}
	if m := (*sent)[1]; m.Stage != updateStageFailed || strings.Contains(m.Message, "\n") {
		t.Fatalf("failure = %+v, want one line", m)
	}
}

func TestUpdateReporterBoundsTheMessage(t *testing.T) {
	sent := captureServerMessages(t)
	u := newUpdateReporter(updateFromServer, "1.0.0")
	u.stage(updateStageFailed, strings.Repeat("é", 400))
	if n := len((*sent)[0].Message); n > updateMessageMax {
		t.Fatalf("message is %d bytes, want at most %d", n, updateMessageMax)
	}
}

// The download is reported every 10 percent, and its end always.
func TestUpdateReporterThrottlesPercent(t *testing.T) {
	sent := captureServerMessages(t)
	u := newUpdateReporter(updateFromServer, "1.0.0")
	for _, p := range []int{0, 3, 9, 10, 15, 21, 95, 99, 100, 100, 140} {
		u.percent(p)
	}
	var got []int
	for _, m := range *sent {
		if m.Stage != updateStageDownloading || m.Percent == nil {
			t.Fatalf("unexpected message %+v", m)
		}
		got = append(got, *m.Percent)
	}
	want := []int{0, 10, 21, 95, 100}
	if len(got) != len(want) {
		t.Fatalf("percents = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("percents = %v, want %v", got, want)
		}
	}
}

func TestProgressReaderReportsAgainstTheTotal(t *testing.T) {
	var reported []int
	r := &progressReader{r: strings.NewReader(strings.Repeat("x", 1000)), total: 1000, report: func(p int) { reported = append(reported, p) }}
	buf := make([]byte, 250)
	for {
		if _, err := r.Read(buf); err == io.EOF {
			break
		}
	}
	if len(reported) != 4 || reported[3] != 100 {
		t.Fatalf("reported = %v, want 25,50,75,100", reported)
	}

	// Size not known: nothing to report.
	reported = nil
	r = &progressReader{r: strings.NewReader("abc"), report: func(p int) { reported = append(reported, p) }}
	_, _ = io.ReadAll(r)
	if len(reported) != 0 {
		t.Fatalf("reported %v without a total", reported)
	}
}

func TestDownloadTotal(t *testing.T) {
	sized := func(v string) http.Header {
		h := http.Header{}
		h.Set(agentBinarySizeHeader, v)
		return h
	}
	cases := []struct {
		name string
		resp *http.Response
		want int64
	}{
		{"size header wins over a compressed length", &http.Response{Header: sized("5000"), ContentLength: -1, Uncompressed: true}, 5000},
		{"plain body uses its length", &http.Response{Header: http.Header{}, ContentLength: 1234}, 1234},
		{"decompressed body without the header is unknown", &http.Response{Header: http.Header{}, ContentLength: -1, Uncompressed: true}, 0},
		{"a bad header falls back", &http.Response{Header: sized("-3"), ContentLength: 10}, 10},
	}
	for _, c := range cases {
		if got := downloadTotal(c.resp); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}
