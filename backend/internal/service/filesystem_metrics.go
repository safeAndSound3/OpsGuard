package service

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FilesystemMetric is one persistent, host-visible filesystem. Container and
// runtime filesystems are deliberately excluded before they reach the UI.
type FilesystemMetric struct {
	Filesystem  string  `json:"filesystem"`
	MountPoint  string  `json:"mountPoint"`
	TotalBytes  int64   `json:"totalBytes"`
	UsedBytes   int64   `json:"usedBytes"`
	UsedPercent float64 `json:"usedPercent"`
}

func parseFilesystemMetrics(raw string) []FilesystemMetric {
	items := make([]FilesystemMetric, 0)
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[0] == "Filesystem" {
			continue
		}
		total, totalErr := strconv.ParseInt(fields[1], 10, 64)
		used, usedErr := strconv.ParseInt(fields[2], 10, 64)
		if totalErr != nil || usedErr != nil || total <= 0 {
			continue
		}
		mountPoint := strings.Join(fields[5:], " ")
		if excludedFilesystem(fields[0], mountPoint) {
			continue
		}
		items = append(items, FilesystemMetric{
			Filesystem: fields[0], MountPoint: mountPoint, TotalBytes: total, UsedBytes: used,
			UsedPercent: float64(used) / float64(total) * 100,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UsedPercent == items[j].UsedPercent {
			return items[i].MountPoint < items[j].MountPoint
		}
		return items[i].UsedPercent > items[j].UsedPercent
	})
	return items
}

func excludedFilesystem(filesystem, mountPoint string) bool {
	fs := strings.ToLower(strings.TrimSpace(filesystem))
	mount := strings.TrimRight(strings.TrimSpace(mountPoint), "/")
	if mount == "" {
		mount = "/"
	}
	if containerRuntimeMount(mount) {
		return true
	}
	for _, value := range []string{"tmpfs", "devtmpfs", "overlay", "squashfs", "nsfs", "proc", "sysfs", "cgroup", "cgroup2"} {
		if fs == value || strings.HasPrefix(fs, value+":") {
			return true
		}
	}
	return false
}

// containerRuntimeMount excludes storage and bind mounts owned by container
// runtimes. Matching path segments, rather than only /var/lib/docker, also
// covers common custom roots such as /docker and /data/containerd.
func containerRuntimeMount(mountPoint string) bool {
	for _, segment := range strings.Split(strings.Trim(mountPoint, "/"), "/") {
		switch strings.ToLower(strings.TrimSpace(segment)) {
		case "docker", "containerd", "containers", "kubelet", "kubernetes", "podman", "k3s", "rancher":
			return true
		}
	}
	return false
}

func storeFilesystemMetrics(sourceID string, items []FilesystemMetric, collectedAt time.Time) error {
	metricsMu.RLock()
	current := metricsDB
	metricsMu.RUnlock()
	if current == nil {
		return fmt.Errorf("metrics store is not initialized")
	}
	if len(items) == 0 {
		return nil
	}
	tx, err := current.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO host_filesystem_samples (source_id, filesystem, mount_point, total_bytes, used_bytes, used_percent, collected_at) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, item := range items {
		if _, err := stmt.Exec(sourceID, item.Filesystem, item.MountPoint, item.TotalBytes, item.UsedBytes, item.UsedPercent, collectedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func LatestHostFilesystemMetrics(sourceID string) ([]FilesystemMetric, time.Time, error) {
	metricsMu.RLock()
	current := metricsDB
	metricsMu.RUnlock()
	if current == nil {
		return nil, time.Time{}, fmt.Errorf("metrics store is not initialized")
	}
	var collectedAt time.Time
	if err := current.QueryRow(`SELECT collected_at FROM host_filesystem_samples WHERE source_id = ? ORDER BY collected_at DESC, id DESC LIMIT 1`, sourceID).Scan(&collectedAt); err != nil {
		return nil, time.Time{}, err
	}
	rows, err := current.Query(`SELECT filesystem, mount_point, total_bytes, used_bytes, used_percent FROM host_filesystem_samples WHERE source_id = ? AND collected_at = ? ORDER BY used_percent DESC, mount_point ASC`, sourceID, collectedAt)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	items := []FilesystemMetric{}
	for rows.Next() {
		var item FilesystemMetric
		if err := rows.Scan(&item.Filesystem, &item.MountPoint, &item.TotalBytes, &item.UsedBytes, &item.UsedPercent); err != nil {
			return nil, time.Time{}, err
		}
		// Apply the same policy to retained samples so an older collection made
		// before a filter update cannot reappear in the dashboard.
		if excludedFilesystem(item.Filesystem, item.MountPoint) {
			continue
		}
		items = append(items, item)
	}
	return items, collectedAt, rows.Err()
}
