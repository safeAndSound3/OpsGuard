package service

import (
	"monitor-platform/internal/model"
	"net/url"
	"testing"
)

func TestHadoopLogURLBelongsToSource(t *testing.T) {
	source := model.DataSource{Options: map[string]string{
		"nodeManagerUrl": "http://127.0.0.1:6064",
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
		{name: "internal node manager log is rewritten", url: "http://hadoop-node:8042/node/containerlogs/container_1", want: true},
		{name: "raw node manager origin is rewritten", url: "http://other-cluster:8042/node/containerlogs/container_1", want: true},
		{name: "unrelated path", url: "http://other-cluster:8042/admin", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hadoopLogURLBelongsToSource(source, base, tc.url); got != tc.want {
				t.Fatalf("hadoopLogURLBelongsToSource(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
	if got, want := hadoopNodeManagerLogURL(source, "http://other-cluster:8042/node/containerlogs/container_1?start=0"), "http://127.0.0.1:6064/node/containerlogs/container_1?start=0"; got != want {
		t.Fatalf("hadoopNodeManagerLogURL() = %q, want %q", got, want)
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
