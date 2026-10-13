package service

import (
	"monitor-platform/internal/model"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestHadoopLogURLBelongsToSource(t *testing.T) {
	source := model.DataSource{Options: map[string]string{
		"nodeManagerUrl": "http://127.0.0.1:6064, http://127.0.0.1:6065",
		"jobHistoryUrl":  "http://127.0.0.1:6063",
	}}
	base := "http://127.0.0.1:6062"
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{name: "resource manager", url: base + "/logs/app", want: true},
		{name: "configured node manager", url: "http://127.0.0.1:6064/node/containerlogs/container_1", want: true},
		{name: "configured history server", url: "http://127.0.0.1:6063/jobhistory/logs/container_1", want: true},
		{name: "second configured node manager", url: "http://127.0.0.1:6065/node/containerlogs/container_1", want: true},
		{name: "internal node manager is routed through configured nodes", url: "http://other-cluster:8042/node/containerlogs/container_1", want: true},
		{name: "unrelated path", url: "http://other-cluster:8042/admin", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hadoopLogURLBelongsToSource(source, base, tc.url); got != tc.want {
				t.Fatalf("hadoopLogURLBelongsToSource(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
	urls := hadoopNodeManagerLogURLs(source, "http://127.0.0.1:6064/node/containerlogs/container_1?start=0")
	if got, want := strings.Join(urls, ","), "http://127.0.0.1:6064/node/containerlogs/container_1?start=0,http://127.0.0.1:6065/node/containerlogs/container_1?start=0"; got != want {
		t.Fatalf("hadoopNodeManagerLogURLs() = %q, want %q", got, want)
	}
}

func TestHadoopContainerLogFileURLs(t *testing.T) {
	page, err := url.Parse("http://127.0.0.1:8042/node/containerlogs/container_1/root")
	if err != nil {
		t.Fatal(err)
	}
	body := `<a href="/node/containerlogs/container_1/root/AppMaster.stderr/?start=4096">stderr</a>
<a href="/node/containerlogs/container_1/root/AppMaster.stdout/?start=4096">stdout</a>
<a href="/node/containerlogs/container_1/root/jobmanager.log/?start=4096">jobmanager</a>
<a href="/node/containerlogs/container_1/root/directory.info/?start=4096">directory</a>`
	got := hadoopContainerLogFileURLs(page, body)
	if len(got) != 4 {
		t.Fatalf("hadoopContainerLogFileURLs() returned %d URLs, want 4: %#v", len(got), got)
	}
	if got[0] != "http://127.0.0.1:8042/node/containerlogs/container_1/root/AppMaster.stderr/?start=0" {
		t.Fatalf("first log URL = %q", got[0])
	}
	if hadoopContainerLogFileName(got[1]) != "AppMaster.stdout" {
		t.Fatalf("log file name = %q", hadoopContainerLogFileName(got[1]))
	}
}

func TestHadoopContainerLogUnavailable(t *testing.T) {
	if !hadoopContainerLogUnavailable([]byte("<h1>No logs available for container container_01</h1>")) {
		t.Fatal("missing-container page must be treated as an unavailable NodeManager response")
	}
	if hadoopContainerLogUnavailable([]byte("LogType:stdout\nworker output")) {
		t.Fatal("a regular container log must remain readable")
	}
}

func TestHadoopAggregatedLogURLs(t *testing.T) {
	source := model.DataSource{Options: map[string]string{"jobHistoryUrl": "http://history:19888"}}
	urls := hadoopAggregatedLogURLs(source, "http://ga-k8s2:8042/node/containerlogs/container_e383_01_000001/root")
	if got, want := strings.Join(urls, ","), "http://history:19888/jobhistory/logs/ga-k8s2:8042/container_e383_01_000001/container_e383_01_000001/root"; got != want {
		t.Fatalf("hadoopAggregatedLogURLs() = %q, want %q", got, want)
	}
}

func TestHadoopContainerLogFileURLsKeepsEveryFile(t *testing.T) {
	page, err := url.Parse("http://127.0.0.1:8042/node/containerlogs/container_1/root")
	if err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for i := 0; i < 32; i++ {
		body.WriteString(`<a href="/node/containerlogs/container_1/root/log-`)
		body.WriteString(strconv.Itoa(i))
		body.WriteString(`">log</a>`)
	}
	if got := hadoopContainerLogFileURLs(page, body.String()); len(got) != 32 {
		t.Fatalf("hadoopContainerLogFileURLs() returned %d URLs, want 32", len(got))
	}
}

func TestReadableHadoopLogKeepsJobHistoryFileSections(t *testing.T) {
	body := []byte(`<html><pre>LogType:stdout
Log Contents:
worker output</pre><pre>LogType:stderr
Log Contents:
worker error</pre></html>`)
	got := readableHadoopLog(body)
	for _, heading := range []string{"===== stdout =====", "===== stderr ====="} {
		if !strings.Contains(got, heading) {
			t.Fatalf("readableHadoopLog() missing %q: %s", heading, got)
		}
	}
}

func TestMergeHadoopContainersRetainsWorkersAfterTerminalResponse(t *testing.T) {
	liveAM := model.HadoopContainer{ID: "container_01_0001_01_000001", State: "COMPLETED", LogURL: "http://live/am"}
	savedWorkerOne := model.HadoopContainer{ID: "container_01_0001_01_000002", State: "COMPLETE", LogURL: "http://history/worker-1"}
	savedWorkerTwo := model.HadoopContainer{ID: "container_01_0001_01_000003", State: "COMPLETE", LogURL: "http://history/worker-2"}
	// The RM response for an ended application has only the AM. Its current
	// URL wins, while both historical worker containers remain selectable.
	got := mergeHadoopContainers([]model.HadoopContainer{liveAM}, []model.HadoopContainer{
		{ID: liveAM.ID, State: "RUNNING", LogURL: "http://stale/am"}, savedWorkerOne, savedWorkerTwo,
	})
	if len(got) != 3 {
		t.Fatalf("mergeHadoopContainers() returned %d containers, want 3: %#v", len(got), got)
	}
	if got[0].LogURL != liveAM.LogURL {
		t.Fatalf("live AM URL should take precedence, got %q", got[0].LogURL)
	}
	if got[1].ID != savedWorkerOne.ID || got[2].ID != savedWorkerTwo.ID {
		t.Fatalf("worker containers were not retained: %#v", got)
	}
}

func TestHadoopContainerLogURLForID(t *testing.T) {
	amURL := "http://node:8042/node/containerlogs/container_1_0002_01_000001/root?start=0"
	if got, want := hadoopContainerLogURLForID(amURL, "container_1_0002_01_000004"), "http://node:8042/node/containerlogs/container_1_0002_01_000004/root?start=0"; got != want {
		t.Fatalf("hadoopContainerLogURLForID() = %q, want %q", got, want)
	}
	ids := hadoopContainerID.FindAllString("containerId=container_1_0002_01_000002 containerId=container_1_0002_01_000003", -1)
	if len(ids) != 2 || ids[1] != "container_1_0002_01_000003" {
		t.Fatalf("container ID parsing = %#v", ids)
	}
}
