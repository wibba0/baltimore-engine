package ui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func start(t *testing.T) (base, token string) {
	t.Helper()
	s := New(Config{BundledData: "../engine/testdata", UserData: t.TempDir()})
	u, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	return "http://" + pu.Host, pu.Query().Get("t")
}

func call(t *testing.T, method, u, token string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, u, rd)
	if token != "" {
		req.Header.Set("X-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func TestTokenRequired(t *testing.T) {
	base, _ := start(t)
	resp, _ := call(t, "GET", base+"/api/info", "", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d without token, want 403", resp.StatusCode)
	}
	resp, _ = call(t, "GET", base+"/api/info", "wrong", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d with bad token, want 403", resp.StatusCode)
	}
}

func TestIndexAndRun(t *testing.T) {
	base, token := start(t)

	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	var page bytes.Buffer
	page.ReadFrom(resp.Body)
	resp.Body.Close()
	if !strings.Contains(page.String(), "Baltimore Intelligence Engine") || strings.Contains(page.String(), "/*REPORT_CSS*/") {
		t.Fatal("index page missing title or CSS was not injected")
	}

	_, info := call(t, "GET", base+"/api/info", token, nil)
	if info["dataReady"] != true || len(info["questions"].([]any)) != 5 {
		t.Fatalf("info = %v", info)
	}

	resp, out := call(t, "POST", base+"/api/run", token, map[string]any{"questions": []int{1, 2, 3, 4, 5}, "workers": 3})
	if resp.StatusCode != 200 {
		t.Fatalf("run status %d: %v", resp.StatusCode, out)
	}
	secs := out["sections"].([]any)
	if len(secs) != 5 || out["records"].(float64) < 10 {
		t.Fatalf("run returned %d sections, %v records", len(secs), out["records"])
	}
	if !strings.Contains(secs[0].(map[string]any)["html"].(string), "<table>") {
		t.Error("section html has no table")
	}

	resp, _ = call(t, "POST", base+"/api/run", token, map[string]any{"questions": []int{}, "workers": 2})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty selection status %d, want 400", resp.StatusCode)
	}
}

func TestLineLogCarriageReturn(t *testing.T) {
	l := &lineLog{max: 10}
	l.Write([]byte("fetch crime\n\r  1/3 pages\r  2/3 pages\r  wrote crime.csv\n"))
	got := l.lines()
	if len(got) != 2 || got[1] != "wrote crime.csv" {
		t.Fatalf("lines = %q", got)
	}
}
