package service

import "testing"

func TestExcludedFilesystem(t *testing.T) {
	tests := []struct {
		name       string
		filesystem string
		mount      string
		excluded   bool
	}{
		{name: "root", filesystem: "/dev/vda1", mount: "/", excluded: false},
		{name: "custom docker root", filesystem: "/dev/vdb1", mount: "/docker", excluded: true},
		{name: "nested docker root", filesystem: "/dev/vdb1", mount: "/data/docker/volumes", excluded: true},
		{name: "containerd root", filesystem: "/dev/vdb1", mount: "/data/containerd", excluded: true},
		{name: "kubelet root", filesystem: "/dev/vdb1", mount: "/var/lib/kubelet/pods", excluded: true},
		{name: "ordinary data", filesystem: "/dev/vdb1", mount: "/data/application", excluded: false},
		{name: "overlay filesystem", filesystem: "overlay", mount: "/", excluded: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := excludedFilesystem(test.filesystem, test.mount); got != test.excluded {
				t.Fatalf("excludedFilesystem(%q, %q) = %t, want %t", test.filesystem, test.mount, got, test.excluded)
			}
		})
	}
}
