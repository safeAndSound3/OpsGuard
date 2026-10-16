package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"monitor-platform/internal/model"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	hadoopLogPreBlocks     = regexp.MustCompile(`(?is)<pre[^>]*>(.*?)</pre>`)
	hadoopLogLinks         = regexp.MustCompile(`(?is)<a[^>]+href\s*=\s*["']([^"']+)["'][^>]*>`)
	hadoopLogType          = regexp.MustCompile(`(?im)^\s*LogType\s*:\s*([^\r\n]+)\s*$`)
	hadoopContainerID      = regexp.MustCompile(`\bcontainer_\d+_\d+_\d+_\d+\b`)
	hadoopSnapshotJobs     sync.Map
	hadoopSynchronizerOnce sync.Once
)

type HadoopApplicationQuery struct {
	Page        int
	PageSize    int
	Keyword     string
	User        string
	Type        string
	State       string
	FinalStatus string
}

// startHadoopApplicationSynchronizer keeps application and active-container
// snapshots current even while nobody has the Hadoop page open. UI requests
// still call ListHadoopApplications directly and therefore remain immediate.
func startHadoopApplicationSynchronizer() {
	hadoopSynchronizerOnce.Do(func() {
		go func() {
			for {
				for _, source := range ListDataSources() {
					if source.Enabled && strings.EqualFold(source.Type, "hadoop") {
						_, _ = ListHadoopApplications(source.ID, HadoopApplicationQuery{Page: 1, PageSize: 20})
					}
				}
				time.Sleep(RefreshSettingsDuration("hadoop"))
			}
		}()
	})
}

func ListHadoopApplications(sourceID string, query HadoopApplicationQuery) (model.HadoopApplicationPage, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil {
		return model.HadoopApplicationPage{}, err
	}
	if !strings.EqualFold(ds.Type, "hadoop") {
		return model.HadoopApplicationPage{}, errors.New("该数据源不是 Hadoop")
	}
	base, err := hadoopBaseURL(ds)
	if err != nil {
		return model.HadoopApplicationPage{}, err
	}
	var payload struct {
		Apps struct {
			App []model.HadoopApplication `json:"app"`
		} `json:"apps"`
	}
	// Bound remote and local reads so a long-lived cluster cannot turn page
	// refreshes into unbounded scans. The durable snapshot remains the fallback.
	rmErr := hadoopGetJSON(base+"/ws/v1/cluster/apps?limit=1000", &payload)
	applications := make(map[string]struct{}, len(payload.Apps.App))
	all := make([]model.HadoopApplication, 0, len(payload.Apps.App))
	for _, app := range payload.Apps.App {
		applications[app.ID] = struct{}{}
		all = append(all, app)
	}
	for _, app := range listHadoopHistoryApplications(ds) {
		if _, exists := applications[app.ID]; !exists {
			applications[app.ID] = struct{}{}
			all = append(all, app)
		}
	}
	// Persist every observation and use the snapshot as the durable source for
	// historical tasks when ResourceManager or JobHistory is temporarily down.
	_ = upsertHadoopApplicationSnapshots(sourceID, all)
	// ResourceManager can discard completed-container records shortly after an
	// application stops. Capture active applications while their full lists are
	// still available so completed logs remain selectable later.
	scheduleHadoopActiveContainerSnapshots(sourceID, all)
	if snapshots, err := listHadoopApplicationSnapshots(sourceID); err == nil {
		all = snapshots
	}
	if rmErr != nil && len(all) == 0 {
		return model.HadoopApplicationPage{}, rmErr
	}
	// ResourceManager and JobHistory use independent return orders after merging.
	sort.SliceStable(all, func(i, j int) bool { return all[i].ID > all[j].ID })
	facets := map[string][]string{"user": {}, "applicationType": {}, "state": {}, "finalStatus": {}}
	seenFacets := map[string]map[string]bool{"user": {}, "applicationType": {}, "state": {}, "finalStatus": {}}
	filtered := make([]model.HadoopApplication, 0, len(all))
	for _, app := range all {
		for key, value := range map[string]string{"user": app.User, "applicationType": app.Type, "state": app.State, "finalStatus": app.FinalStatus} {
			if value != "" && !seenFacets[key][value] {
				facets[key] = append(facets[key], value)
				seenFacets[key][value] = true
			}
		}
		if hadoopApplicationMatches(app, query) {
			filtered = append(filtered, app)
		}
	}
	for key := range facets {
		sort.Strings(facets[key])
	}
	pageSize := query.PageSize
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	page := query.Page
	if page < 1 {
		page = 1
	}
	start := (page - 1) * pageSize
	items := []model.HadoopApplication{}
	if start < len(filtered) {
		end := start + pageSize
		if end > len(filtered) {
			end = len(filtered)
		}
		items = filtered[start:end]
	}
	return model.HadoopApplicationPage{Items: items, Total: len(filtered), Facets: facets}, nil
}

type hadoopSnapshotJob struct {
	mu      sync.Mutex
	lastRun time.Time
}

func scheduleHadoopActiveContainerSnapshots(sourceID string, apps []model.HadoopApplication) {
	value, _ := hadoopSnapshotJobs.LoadOrStore(sourceID, &hadoopSnapshotJob{})
	job := value.(*hadoopSnapshotJob)
	job.mu.Lock()
	if time.Since(job.lastRun) < 30*time.Second {
		job.mu.Unlock()
		return
	}
	job.lastRun = time.Now()
	job.mu.Unlock()
	active := make([]model.HadoopApplication, 0, 100)
	for _, app := range apps {
		if hadoopApplicationActive(app) && strings.TrimSpace(app.ID) != "" {
			active = append(active, app)
			if len(active) == 100 {
				break
			}
		}
	}
	if len(active) == 0 {
		return
	}
	go snapshotHadoopActiveContainers(sourceID, active)
}

func snapshotHadoopActiveContainers(sourceID string, apps []model.HadoopApplication) {
	workers := make(chan struct{}, 4)
	var group sync.WaitGroup
	for _, app := range apps {
		workers <- struct{}{}
		group.Add(1)
		go func(applicationID string) {
			defer group.Done()
			defer func() { <-workers }()
			_, _ = ListHadoopContainers(sourceID, applicationID)
		}(app.ID)
	}
	group.Wait()
}

func upsertHadoopApplicationSnapshots(sourceID string, apps []model.HadoopApplication) error {
	current := currentStore()
	if current == nil {
		return errors.New("Hadoop snapshot store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := current.ExecContext(ctx, `DELETE FROM hadoop_application_snapshots WHERE source_id = ? AND last_seen_at < DATE_SUB(CURRENT_TIMESTAMP, INTERVAL 180 DAY)`, sourceID); err != nil {
		return err
	}
	for _, app := range apps {
		if strings.TrimSpace(app.ID) == "" {
			continue
		}
		raw, err := json.Marshal(app)
		if err != nil {
			continue
		}
		if _, err := current.ExecContext(ctx, `INSERT INTO hadoop_application_snapshots (source_id, application_id, application_json) VALUES (?, ?, ?)
			ON DUPLICATE KEY UPDATE application_json = VALUES(application_json), last_seen_at = CURRENT_TIMESTAMP`, sourceID, app.ID, raw); err != nil {
			return err
		}
	}
	return nil
}

func listHadoopApplicationSnapshots(sourceID string) ([]model.HadoopApplication, error) {
	current := currentStore()
	if current == nil {
		return nil, errors.New("Hadoop snapshot store is not initialized")
	}
	rows, err := current.Query(`SELECT application_json FROM hadoop_application_snapshots WHERE source_id = ? ORDER BY last_seen_at DESC LIMIT 5000`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.HadoopApplication, 0)
	for rows.Next() {
		var raw []byte
		var app model.HadoopApplication
		if rows.Scan(&raw) == nil && json.Unmarshal(raw, &app) == nil {
			items = append(items, app)
		}
	}
	return items, rows.Err()
}

func upsertHadoopContainerSnapshot(sourceID, appID string, containers []model.HadoopContainer) error {
	if len(containers) == 0 {
		return nil
	}
	current := currentStore()
	if current == nil {
		return errors.New("Hadoop snapshot store is not initialized")
	}
	// A terminal ResourceManager response commonly retains only the AM
	// container. Never let that partial response replace the full list captured
	// while the application was running.
	if saved, err := listHadoopContainerSnapshot(sourceID, appID); err == nil {
		containers = mergeHadoopContainers(containers, saved)
	}
	raw, err := json.Marshal(containers)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err = current.ExecContext(ctx, `DELETE FROM hadoop_container_snapshots WHERE source_id = ? AND last_seen_at < DATE_SUB(CURRENT_TIMESTAMP, INTERVAL 180 DAY)`, sourceID); err != nil {
		return err
	}
	_, err = current.ExecContext(ctx, `INSERT INTO hadoop_container_snapshots (source_id, application_id, container_json) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE container_json = VALUES(container_json), last_seen_at = CURRENT_TIMESTAMP`, sourceID, appID, raw)
	return err
}

func listHadoopContainerSnapshot(sourceID, appID string) ([]model.HadoopContainer, error) {
	current := currentStore()
	if current == nil {
		return nil, errors.New("Hadoop snapshot store is not initialized")
	}
	var raw []byte
	if err := current.QueryRow(`SELECT container_json FROM hadoop_container_snapshots WHERE source_id = ? AND application_id = ?`, sourceID, appID).Scan(&raw); err != nil {
		return nil, err
	}
	var containers []model.HadoopContainer
	if err := json.Unmarshal(raw, &containers); err != nil {
		return nil, err
	}
	return containers, nil
}

func hadoopContainerSnapshotOrError(sourceID, appID string, fallback error) ([]model.HadoopContainer, error) {
	if containers, err := listHadoopContainerSnapshot(sourceID, appID); err == nil && len(containers) > 0 {
		return containers, nil
	}
	if fallback == nil {
		return []model.HadoopContainer{}, nil
	}
	return nil, fallback
}

// mergeHadoopContainers keeps the live ResourceManager data for duplicate IDs
// while retaining containers that ResourceManager has already discarded.
func mergeHadoopContainers(primary, fallback []model.HadoopContainer) []model.HadoopContainer {
	if len(primary) == 0 {
		return append([]model.HadoopContainer(nil), fallback...)
	}
	items := append([]model.HadoopContainer(nil), primary...)
	seen := make(map[string]struct{}, len(primary)+len(fallback))
	for _, container := range primary {
		if id := strings.TrimSpace(container.ID); id != "" {
			seen[id] = struct{}{}
		}
	}
	for _, container := range fallback {
		id := strings.TrimSpace(container.ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		items = append(items, container)
	}
	return items
}

func hadoopApplicationActive(app model.HadoopApplication) bool {
	return !strings.EqualFold(app.State, "FINISHED") && !strings.EqualFold(app.State, "FAILED") && !strings.EqualFold(app.State, "KILLED")
}

func hadoopApplicationMatches(app model.HadoopApplication, query HadoopApplicationQuery) bool {
	keyword := strings.ToLower(strings.TrimSpace(query.Keyword))
	if keyword != "" && !strings.Contains(strings.ToLower(strings.Join([]string{app.ID, app.Name, app.User, app.Type, app.State, app.FinalStatus}, " ")), keyword) {
		return false
	}
	return (query.User == "" || query.User == app.User) && (query.Type == "" || query.Type == app.Type) && (query.State == "" || query.State == app.State) && (query.FinalStatus == "" || query.FinalStatus == app.FinalStatus)
}

func listHadoopHistoryApplications(ds model.DataSource) []model.HadoopApplication {
	endpoints := hadoopEndpointURLs(ds.Options["jobHistoryUrl"])
	if len(endpoints) == 0 {
		return nil
	}
	var payload struct {
		Jobs struct {
			Job []struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				User       string `json:"user"`
				Queue      string `json:"queue"`
				State      string `json:"state"`
				StartTime  int64  `json:"startTime"`
				FinishTime int64  `json:"finishTime"`
			} `json:"job"`
		} `json:"jobs"`
	}
	var err error
	for _, endpoint := range endpoints {
		payload.Jobs.Job = nil
		err = hadoopGetJSON(endpoint+"/ws/v1/history/mapreduce/jobs?limit=1000", &payload)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil
	}
	items := make([]model.HadoopApplication, 0, len(payload.Jobs.Job))
	for _, job := range payload.Jobs.Job {
		id := strings.Replace(job.ID, "job_", "application_", 1)
		state := strings.ToUpper(job.State)
		if state == "" {
			state = "FINISHED"
		}
		items = append(items, model.HadoopApplication{ID: id, Name: job.Name, User: job.User, Queue: job.Queue, Type: "MAPREDUCE", State: "FINISHED", FinalStatus: state, Progress: 100, StartedTime: job.StartTime, FinishedTime: job.FinishTime})
	}
	return items
}

func ListHadoopContainers(sourceID, appID string) ([]model.HadoopContainer, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(ds.Type, "hadoop") {
		return nil, errors.New("数据源不是 Hadoop 类型")
	}
	base, err := hadoopBaseURL(ds)
	if err != nil {
		return nil, err
	}
	var attempts struct {
		AppAttempts struct {
			AppAttempt []struct {
				ID              json.RawMessage `json:"id"`
				AppAttemptID    string          `json:"appAttemptId"`
				ContainerID     string          `json:"containerId"`
				NodeHTTPAddress string          `json:"nodeHttpAddress"`
				LogsLink        string          `json:"logsLink"`
			} `json:"appAttempt"`
		} `json:"appAttempts"`
	}
	if err := hadoopGetJSON(base+"/ws/v1/cluster/apps/"+appID+"/appattempts", &attempts); err != nil {
		if saved, snapshotErr := listHadoopContainerSnapshot(sourceID, appID); snapshotErr == nil && len(saved) > 0 {
			return saved, nil
		}
		// 停止任务后 ResourceManager 可能已删除 appattempt；仍尝试从 AM 汇总日志恢复容器。
		if amURL := hadoopApplicationAMLogURL(base, appID); amURL != "" {
			if recovered := recoverHadoopContainersFromAMLog(sourceID, amURL, nil); len(recovered) > 0 {
				_ = upsertHadoopContainerSnapshot(sourceID, appID, recovered)
				return recovered, nil
			}
		}
		return hadoopContainerSnapshotOrError(sourceID, appID, err)
	}
	if len(attempts.AppAttempts.AppAttempt) == 0 {
		items := recoverHadoopContainersFromAMLog(sourceID, hadoopApplicationAMLogURL(base, appID), nil)
		if saved, err := listHadoopContainerSnapshot(sourceID, appID); err == nil {
			items = mergeHadoopContainers(items, saved)
		}
		if len(items) > 0 {
			_ = upsertHadoopContainerSnapshot(sourceID, appID, items)
			return items, nil
		}
		return hadoopContainerSnapshotOrError(sourceID, appID, nil)
	}
	var payload struct {
		Containers struct {
			Container []struct {
				ID              string `json:"containerId"`
				NodeHTTPAddress string `json:"nodeHttpAddress"`
				State           string `json:"containerState"`
				LogURL          string `json:"logUrl"`
				Priority        string `json:"priority"`
				AllocatedMB     int64  `json:"allocatedMB"`
				AllocatedVCores int64  `json:"allocatedVCores"`
			} `json:"container"`
		} `json:"containers"`
		Container []struct {
			ID              string `json:"containerId"`
			NodeHTTPAddress string `json:"nodeHttpAddress"`
			State           string `json:"containerState"`
			LogURL          string `json:"logUrl"`
			Priority        string `json:"priority"`
			AllocatedMB     int64  `json:"allocatedMB"`
			AllocatedVCores int64  `json:"allocatedVCores"`
		} `json:"container"`
	}
	latest := attempts.AppAttempts.AppAttempt[len(attempts.AppAttempts.AppAttempt)-1]
	id := strings.TrimSpace(latest.AppAttemptID)
	if id == "" {
		id, err = hadoopJSONIdentifier(latest.ID)
	}
	if err != nil {
		return nil, err
	}
	if err := hadoopGetJSON(base+"/ws/v1/cluster/apps/"+appID+"/appattempts/"+id+"/containers", &payload); err != nil {
		return hadoopContainerSnapshotOrError(sourceID, appID, err)
	}
	containers := payload.Containers.Container
	if len(containers) == 0 {
		containers = payload.Container
	}
	if len(containers) == 0 && latest.ContainerID != "" {
		items := []model.HadoopContainer{{
			ID:              latest.ContainerID,
			NodeHTTPAddress: latest.NodeHTTPAddress,
			State:           "COMPLETED",
			LogURL:          latest.LogsLink,
			IsAM:            true,
		}}
		items = recoverHadoopContainersFromAMLog(sourceID, latest.LogsLink, items)
		_ = upsertHadoopContainerSnapshot(sourceID, appID, items)
		return items, nil
	}
	items := make([]model.HadoopContainer, 0, len(containers))
	for _, container := range containers {
		items = append(items, model.HadoopContainer{
			ID:              container.ID,
			NodeHTTPAddress: container.NodeHTTPAddress,
			State:           container.State,
			LogURL:          container.LogURL,
			Priority:        container.Priority,
			IsAM:            container.ID == latest.ContainerID,
			AllocatedMB:     container.AllocatedMB,
			AllocatedVCores: container.AllocatedVCores,
			AttemptID:       id,
		})
	}
	// Some ResourceManagers retain only the AM once an application is terminal.
	// DistributedShell's AM log still records every allocated worker ID, so use
	// it to recover selectable worker log links when no live/snapshotted list
	// exists yet.
	if len(items) == 1 {
		items = recoverHadoopContainersFromAMLog(sourceID, items[0].LogURL, items)
	}
	// ResourceManager can return only the AM after an application finishes.
	// Return the current entries first, then the durable running-time snapshot
	// so the user can still select every worker container and download its log.
	if saved, err := listHadoopContainerSnapshot(sourceID, appID); err == nil {
		items = mergeHadoopContainers(items, saved)
	}
	_ = upsertHadoopContainerSnapshot(sourceID, appID, items)
	return items, nil
}

// hadoopApplicationAMLogURL queries the application detail endpoint because
// ResourceManager may remove app-attempt records once an application ends,
// while retaining the ApplicationMaster aggregate-log address.
func hadoopApplicationAMLogURL(base, appID string) string {
	var payload struct {
		App struct {
			AMContainerLogs string `json:"amContainerLogs"`
		} `json:"app"`
	}
	if err := hadoopGetJSON(base+"/ws/v1/cluster/apps/"+url.PathEscape(appID), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.App.AMContainerLogs)
}

func recoverHadoopContainersFromAMLog(sourceID, amLogURL string, containers []model.HadoopContainer) []model.HadoopContainer {
	if strings.TrimSpace(amLogURL) == "" {
		return containers
	}
	content, err := HadoopContainerLog(sourceID, amLogURL)
	if err != nil {
		return containers
	}
	seen := make(map[string]struct{}, len(containers)+1)
	for _, container := range containers {
		if id := strings.TrimSpace(container.ID); id != "" {
			seen[id] = struct{}{}
		}
	}
	amID := hadoopContainerID.FindString(amLogURL)
	if amID != "" {
		if _, exists := seen[amID]; !exists {
			containers = append(containers, model.HadoopContainer{ID: amID, State: "COMPLETED", LogURL: amLogURL, IsAM: true})
			seen[amID] = struct{}{}
		}
	}
	for _, id := range hadoopContainerID.FindAllString(content, -1) {
		if _, exists := seen[id]; exists {
			continue
		}
		logURL := hadoopContainerLogURLForID(amLogURL, id)
		if logURL == "" {
			continue
		}
		seen[id] = struct{}{}
		containers = append(containers, model.HadoopContainer{ID: id, State: "COMPLETED", LogURL: logURL, IsAM: id == amID})
	}
	return containers
}

func hadoopContainerLogURLForID(amLogURL, containerID string) string {
	endpoint, err := url.Parse(strings.TrimSpace(amLogURL))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return ""
	}
	currentID := hadoopContainerID.FindString(endpoint.Path)
	if currentID == "" {
		return ""
	}
	endpoint.Path = strings.Replace(endpoint.Path, currentID, containerID, 1)
	endpoint.RawPath = ""
	return endpoint.String()
}

// YARN implementations differ: appAttempt IDs may be JSON strings or numbers.
func hadoopJSONIdentifier(raw json.RawMessage) (string, error) {
	var id string
	if err := json.Unmarshal(raw, &id); err == nil && strings.TrimSpace(id) != "" {
		return id, nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil && strings.TrimSpace(number.String()) != "" {
		return number.String(), nil
	}
	return "", errors.New("YARN app attempt ID 无效")
}

func HadoopContainerLog(sourceID, logURL string) (string, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(ds.Type, "hadoop") {
		return "", errors.New("数据源不是 Hadoop 类型")
	}
	base, err := hadoopBaseURL(ds)
	if err != nil {
		return "", err
	}
	if !hadoopLogURLBelongsToSource(ds, base, logURL) {
		return "", errors.New("日志地址不属于当前 Hadoop 数据源")
	}
	requestURLs := hadoopNodeManagerLogURLs(ds, logURL)
	client := safeHTTPClient(12 * time.Second)
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, requestURL, err := hadoopLogFromCandidatesWithRedirect(client, ds, requestURLs)
	if err != nil {
		nodeManagerErr := err
		// Completed containers may already have been removed from every
		// NodeManager without returning the usual redirect. In that case use the
		// standard JobHistory aggregated-log URL derived from the container URL.
		resp, requestURL, err = hadoopLogFromCandidatesWithRedirect(client, ds, hadoopAggregatedLogURLs(ds, logURL))
		if err != nil {
			return "", fmt.Errorf("日志服务不可用（NodeManager: %v；JobHistory: %w）", nodeManagerErr, err)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", errors.New(resp.Status)
	}
	body, err := readHadoopLogBody(resp.Body)
	if err != nil {
		return "", err
	}
	return readableHadoopLogFromDirectory(client, ds, base, requestURL, body)
}

func hadoopLogFromCandidatesWithRedirect(client *http.Client, hadoop model.DataSource, candidates []string) (*http.Response, string, error) {
	var lastErr error
	var redirectURLs []string
	seenRedirects := make(map[string]struct{})
	for _, candidate := range candidates {
		resp, requestURL, err := hadoopLogGetFromCandidates(client, []string{candidate})
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
			location, locationErr := resp.Location()
			_ = resp.Body.Close()
			if locationErr != nil {
				lastErr = locationErr
				continue
			}
			for _, target := range hadoopJobHistoryLogURLs(hadoop, location) {
				if _, seen := seenRedirects[target]; !seen {
					seenRedirects[target] = struct{}{}
					redirectURLs = append(redirectURLs, target)
				}
			}
			continue
		}
		return resp, requestURL, nil
	}
	if len(redirectURLs) > 0 {
		resp, requestURL, err := hadoopLogGetFromCandidates(client, redirectURLs)
		if err == nil {
			return resp, requestURL, nil
		}
		lastErr = fmt.Errorf("JobHistory 日志服务不可用：%w", err)
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的 Hadoop 日志地址")
	}
	return nil, "", lastErr
}

func hadoopLogGetFromCandidates(client *http.Client, candidates []string) (*http.Response, string, error) {
	var lastErr error
	for _, candidate := range candidates {
		resp, err := hadoopLogGet(client, candidate)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= http.StatusBadRequest {
			lastErr = errors.New(resp.Status)
			_ = resp.Body.Close()
			continue
		}
		// A NodeManager that does not own the requested container returns a 200
		// HTML page containing this message. It is not a usable log response;
		// continue through the configured NodeManager allowlist instead.
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			body, readErr := readHadoopLogBody(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
				continue
			}
			if hadoopContainerLogUnavailable(body) {
				lastErr = errors.New("该节点未保存此容器日志")
				continue
			}
			resp.Body = io.NopCloser(bytes.NewReader(body))
		}
		return resp, candidate, nil
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的 Hadoop 日志地址")
	}
	return nil, "", lastErr
}

const maxHadoopLogResponseBytes = 16 << 20

func readHadoopLogBody(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxHadoopLogResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxHadoopLogResponseBytes {
		return nil, fmt.Errorf("容器日志超过 %d MiB 读取上限", maxHadoopLogResponseBytes>>20)
	}
	return data, nil
}

func hadoopContainerLogUnavailable(body []byte) bool {
	text := strings.ToLower(string(body))
	return strings.Contains(text, "no logs available for container") ||
		strings.Contains(text, "no logs available for the container") ||
		strings.Contains(text, "container log not found")
}

// hadoopLogGet retries a transient connection reset once. Hadoop's embedded
// Jetty service can briefly reject a request while a container log is rotated.
func hadoopLogGet(client *http.Client, requestURL string) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := client.Get(requestURL)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if attempt == 0 {
			time.Sleep(250 * time.Millisecond)
		}
	}
	return nil, lastErr
}

// hadoopLogURLBelongsToSource prevents a log URL returned by one cluster from
// being requested through another Hadoop datasource. Compare parsed origins
// instead of string prefixes so similarly named hosts cannot bypass the check.
func hadoopLogURLBelongsToSource(hadoop model.DataSource, base, logURL string) bool {
	endpoint, err := url.Parse(strings.TrimSpace(logURL))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return false
	}
	// ResourceManager commonly returns an internal NodeManager hostname while
	// operators configure externally reachable IPs or DNS names. Once explicit
	// NodeManagers are configured, accept only the container-log path and route
	// it through that allowlisted set below; never request the unknown host.
	if strings.HasPrefix(endpoint.Path, "/node/containerlogs/") && len(hadoopEndpointURLs(hadoop.Options["nodeManagerUrl"])) > 0 {
		return true
	}
	// YARN can return a NodeManager's internal hostname. When this datasource
	// has no configured NodeManager endpoint, retain the stricter origin check.
	for _, configured := range append([]string{base}, append(hadoopEndpointURLs(hadoop.Options["nodeManagerUrl"]), hadoopEndpointURLs(hadoop.Options["jobHistoryUrl"])...)...) {
		origin, parseErr := url.Parse(strings.TrimSpace(configured))
		if parseErr == nil && strings.EqualFold(endpoint.Scheme, origin.Scheme) && strings.EqualFold(endpoint.Host, origin.Host) {
			return true
		}
	}
	return false
}

// JobHistory serves aggregated logs as an HTML page. Keep the individual log
// blocks, but return plain text so the platform log viewer remains readable.
func readableHadoopLog(body []byte) string {
	text := string(body)
	if !strings.Contains(strings.ToLower(text), "<html") {
		return text
	}
	blocks := hadoopLogPreBlocks.FindAllStringSubmatch(text, -1)
	if len(blocks) == 0 {
		return text
	}
	sections := make([]string, 0, len(blocks))
	for _, block := range blocks {
		value := strings.TrimSpace(html.UnescapeString(block[1]))
		if value != "" {
			if match := hadoopLogType.FindStringSubmatch(value); len(match) == 2 {
				sections = append(sections, "===== "+strings.TrimSpace(match[1])+" =====\n"+value)
			} else {
				sections = append(sections, value)
			}
		}
	}
	if len(sections) == 0 {
		return text
	}
	return strings.Join(sections, "\n\n")
}

// A running container's NodeManager URL returns an HTML index of files rather
// than log text. Follow the relevant file links and present their content.
func readableHadoopLogFromDirectory(client *http.Client, hadoop model.DataSource, base, pageURL string, body []byte) (string, error) {
	text := string(body)
	page, err := url.Parse(pageURL)
	if err != nil || !strings.HasPrefix(page.Path, "/node/containerlogs/") || !strings.Contains(strings.ToLower(text), "<html") {
		return readableHadoopLog(body), nil
	}
	files := hadoopContainerLogFileURLs(page, text)
	if len(files) == 0 {
		return readableHadoopLog(body), nil
	}
	sections := make([]string, 0, len(files))
	var lastErr error
	const maxCombinedLogBytes = 32 << 20
	combinedBytes := 0
	for _, fileURL := range files {
		if !hadoopLogURLBelongsToSource(hadoop, base, fileURL) {
			lastErr = errors.New("日志文件地址不属于当前 Hadoop 数据源")
			continue
		}
		resp, _, getErr := hadoopLogGetFromCandidates(client, hadoopNodeManagerLogURLs(hadoop, fileURL))
		if getErr != nil {
			lastErr = getErr
			continue
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			lastErr = errors.New(resp.Status)
			_ = resp.Body.Close()
			continue
		}
		content, readErr := readHadoopLogBody(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		combinedBytes += len(content)
		if combinedBytes > maxCombinedLogBytes {
			return "", errors.New("容器日志总量超过 32 MiB 读取上限")
		}
		value := readableHadoopLog(content)
		if strings.Contains(strings.ToLower(string(content)), "<pre") && len(hadoopLogPreBlocks.FindAllStringSubmatch(string(content), -1)) > 0 && strings.Contains(strings.ToLower(value), "<html") {
			value = ""
		}
		if strings.Contains(strings.ToLower(value), "<html") {
			continue
		}
		name := hadoopContainerLogFileName(fileURL)
		if value == "" {
			value = "（空文件）"
		}
		section := "===== " + name + " =====\n" + value
		sections = append(sections, section)
	}
	if len(sections) == 0 {
		if lastErr != nil {
			return "", fmt.Errorf("容器日志目录中的文件读取失败: %w", lastErr)
		}
		return "容器日志目录存在，但暂未生成可读取的 stdout、stderr 或 syslog。", nil
	}
	result := strings.Join(sections, "\n\n")
	return result, nil
}

func hadoopContainerLogFileURLs(page *url.URL, body string) []string {
	seen := make(map[string]struct{})
	urls := make([]string, 0, 3)
	for _, match := range hadoopLogLinks.FindAllStringSubmatch(body, -1) {
		reference, err := url.Parse(html.UnescapeString(match[1]))
		if err != nil {
			continue
		}
		endpoint := page.ResolveReference(reference)
		if !strings.HasPrefix(endpoint.Path, "/node/containerlogs/") {
			continue
		}
		query := endpoint.Query()
		query.Set("start", "0")
		endpoint.RawQuery = query.Encode()
		value := endpoint.String()
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		urls = append(urls, value)
	}
	return urls
}

func hadoopContainerLogFileName(logURL string) string {
	endpoint, err := url.Parse(logURL)
	if err != nil {
		return "container.log"
	}
	path := strings.Trim(strings.TrimSpace(endpoint.Path), "/")
	if path == "" {
		return "container.log"
	}
	return path[strings.LastIndex(path, "/")+1:]
}

func hadoopNodeManagerLogURLs(hadoop model.DataSource, logURL string) []string {
	logEndpoint, err := url.Parse(logURL)
	if err != nil || !strings.HasPrefix(logEndpoint.Path, "/node/containerlogs/") {
		return []string{logURL}
	}
	configuredEndpoints := hadoopEndpointURLs(hadoop.Options["nodeManagerUrl"])
	if len(configuredEndpoints) == 0 {
		return []string{logURL}
	}
	items := make([]string, 0, len(configuredEndpoints))
	seen := make(map[string]struct{}, len(configuredEndpoints)+1)
	// Preserve an address only when it is explicitly configured. An internal
	// hostname from ResourceManager is replaced by configured reachable nodes.
	for _, configured := range configuredEndpoints {
		origin, parseErr := url.Parse(configured)
		if parseErr == nil && strings.EqualFold(logEndpoint.Scheme, origin.Scheme) && strings.EqualFold(logEndpoint.Host, origin.Host) {
			items = append(items, logURL)
			seen[logURL] = struct{}{}
			break
		}
	}
	for _, configured := range configuredEndpoints {
		resolved := configured + logEndpoint.EscapedPath()
		if logEndpoint.RawQuery != "" {
			resolved += "?" + logEndpoint.RawQuery
		}
		if _, exists := seen[resolved]; !exists {
			seen[resolved] = struct{}{}
			items = append(items, resolved)
		}
	}
	return items
}

func hadoopJobHistoryLogURLs(hadoop model.DataSource, target *url.URL) []string {
	if target == nil {
		return nil
	}
	configuredEndpoints := hadoopEndpointURLs(hadoop.Options["jobHistoryUrl"])
	if len(configuredEndpoints) == 0 {
		return []string{target.String()}
	}
	items := make([]string, 0, len(configuredEndpoints))
	seen := make(map[string]struct{}, len(configuredEndpoints))
	for _, configured := range configuredEndpoints {
		base, err := url.Parse(configured)
		if err != nil || base.Scheme == "" || base.Host == "" {
			continue
		}
		base.Path = target.Path
		base.RawPath = target.RawPath
		base.RawQuery = target.RawQuery
		resolved := base.String()
		if _, exists := seen[resolved]; !exists {
			seen[resolved] = struct{}{}
			items = append(items, resolved)
		}
	}
	return items
}

// hadoopAggregatedLogURLs builds JobHistory aggregate-log paths when a
// completed container is no longer retained by its NodeManager. The regular
// redirect is still preferred; this is a fallback for clusters that return a
// 200 "No logs available" page instead of redirecting.
func hadoopAggregatedLogURLs(hadoop model.DataSource, logURL string) []string {
	endpoint, err := url.Parse(logURL)
	if err != nil || endpoint.Host == "" {
		return nil
	}
	parts := strings.Split(strings.Trim(endpoint.Path, "/"), "/")
	containerIndex := -1
	for index, value := range parts {
		if strings.HasPrefix(value, "container_") {
			containerIndex = index
			break
		}
	}
	if containerIndex < 0 || containerIndex+1 >= len(parts) {
		return nil
	}
	containerID := parts[containerIndex]
	user := parts[containerIndex+1]
	if containerID == "" || user == "" {
		return nil
	}
	items := make([]string, 0)
	nodeHosts := []string{endpoint.Host}
	for _, configured := range hadoopEndpointURLs(hadoop.Options["nodeManagerUrl"]) {
		if node, parseErr := url.Parse(configured); parseErr == nil && !strings.EqualFold(node.Host, endpoint.Host) {
			nodeHosts = append(nodeHosts, node.Host)
		}
	}
	seen := make(map[string]struct{})
	for _, base := range hadoopEndpointURLs(hadoop.Options["jobHistoryUrl"]) {
		for _, host := range nodeHosts {
			value := base + "/jobhistory/logs/" + url.PathEscape(host) + "/" + url.PathEscape(containerID) + "/" + url.PathEscape(containerID) + "/" + url.PathEscape(user)
			if _, exists := seen[value]; !exists {
				seen[value] = struct{}{}
				items = append(items, value)
			}
		}
	}
	return items
}

func hadoopGetJSON(url string, target any) error {
	client := safeHTTPClient(10 * time.Second)
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}
