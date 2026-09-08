package service

import (
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
	"time"
)

var (
	hadoopLogPreBlocks = regexp.MustCompile(`(?is)<pre[^>]*>(.*?)</pre>`)
	hadoopLogLinks     = regexp.MustCompile(`(?is)<a[^>]+href\s*=\s*["']([^"']+)["'][^>]*>`)
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

func upsertHadoopApplicationSnapshots(sourceID string, apps []model.HadoopApplication) error {
	current := currentStore()
	if current == nil {
		return errors.New("Hadoop snapshot store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := current.ExecContext(ctx, `DELETE FROM hadoop_application_snapshots WHERE source_id = ? AND last_seen_at < DATE_SUB(CURRENT_TIMESTAMP, INTERVAL 30 DAY)`, sourceID); err != nil {
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
	base := strings.TrimRight(strings.TrimSpace(ds.Options["jobHistoryUrl"]), "/")
	if base == "" {
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
	if err := hadoopGetJSON(base+"/ws/v1/history/mapreduce/jobs?limit=1000", &payload); err != nil {
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

func ListHadoopContainers(sourceID, appID string) (model.HadoopContainerSet, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil {
		return model.HadoopContainerSet{}, err
	}
	if !strings.EqualFold(ds.Type, "hadoop") {
		return model.HadoopContainerSet{}, errors.New("数据源不是 Hadoop 类型")
	}
	base, err := hadoopBaseURL(ds)
	if err != nil {
		return model.HadoopContainerSet{}, err
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
		return model.HadoopContainerSet{}, err
	}
	if len(attempts.AppAttempts.AppAttempt) == 0 {
		return model.HadoopContainerSet{Attempts: []model.HadoopApplicationAttempt{}, Containers: []model.HadoopContainer{}}, nil
	}
	result := model.HadoopContainerSet{Attempts: make([]model.HadoopApplicationAttempt, 0, len(attempts.AppAttempts.AppAttempt)), Containers: []model.HadoopContainer{}}
	for _, attempt := range attempts.AppAttempts.AppAttempt {
		attemptID := strings.TrimSpace(attempt.AppAttemptID)
		if attemptID == "" {
			attemptID, err = hadoopJSONIdentifier(attempt.ID)
		}
		if err != nil {
			continue
		}
		metadata := model.HadoopApplicationAttempt{ID: attemptID, ContainerID: attempt.ContainerID, NodeHTTPAddress: attempt.NodeHTTPAddress, LogURL: attempt.LogsLink}
		result.Attempts = append(result.Attempts, metadata)
		containers, listErr := listHadoopAttemptContainers(base, appID, metadata)
		if listErr != nil {
			continue
		}
		result.Containers = append(result.Containers, containers...)
	}
	return result, nil
}

func listHadoopAttemptContainers(base, appID string, attempt model.HadoopApplicationAttempt) ([]model.HadoopContainer, error) {
	var payload struct {
		Containers struct {
			Container []struct {
				ID              string `json:"containerId"`
				NodeHTTPAddress string `json:"nodeHttpAddress"`
				State           string `json:"containerState"`
				LogURL          string `json:"logUrl"`
				Priority        string `json:"priority"`
			} `json:"container"`
		} `json:"containers"`
		Container []struct {
			ID              string `json:"containerId"`
			NodeHTTPAddress string `json:"nodeHttpAddress"`
			State           string `json:"containerState"`
			LogURL          string `json:"logUrl"`
			Priority        string `json:"priority"`
		} `json:"container"`
	}
	if err := hadoopGetJSON(base+"/ws/v1/cluster/apps/"+appID+"/appattempts/"+attempt.ID+"/containers", &payload); err != nil {
		return nil, err
	}
	containers := payload.Containers.Container
	if len(containers) == 0 {
		containers = payload.Container
	}
	if len(containers) == 0 && attempt.ContainerID != "" {
		return []model.HadoopContainer{{
			ID:              attempt.ContainerID,
			AttemptID:       attempt.ID,
			NodeHTTPAddress: attempt.NodeHTTPAddress,
			State:           "COMPLETED",
			LogURL:          attempt.LogURL,
			IsAM:            true,
		}}, nil
	}
	items := make([]model.HadoopContainer, 0, len(containers))
	for _, container := range containers {
		items = append(items, model.HadoopContainer{
			ID:              container.ID,
			AttemptID:       attempt.ID,
			NodeHTTPAddress: container.NodeHTTPAddress,
			State:           container.State,
			LogURL:          container.LogURL,
			Priority:        container.Priority,
			IsAM:            container.ID == attempt.ContainerID,
		})
	}
	return items, nil
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

func HadoopContainerLog(sourceID, logURL string) (model.HadoopLogContent, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil {
		return model.HadoopLogContent{}, err
	}
	if !strings.EqualFold(ds.Type, "hadoop") {
		return model.HadoopLogContent{}, errors.New("数据源不是 Hadoop 类型")
	}
	base, err := hadoopBaseURL(ds)
	if err != nil {
		return model.HadoopLogContent{}, err
	}
	if !hadoopLogURLBelongsToSource(ds, base, logURL) {
		return model.HadoopLogContent{}, errors.New("日志地址不属于当前 Hadoop 数据源")
	}
	requestURL := hadoopNodeManagerLogURL(ds, logURL)
	client := safeHTTPClient(12 * time.Second)
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hadoopLogGet(client, requestURL)
	if err != nil {
		return model.HadoopLogContent{}, fmt.Errorf("日志服务不可用：%w", err)
	}
	if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
		location, locationErr := resp.Location()
		_ = resp.Body.Close()
		if locationErr != nil {
			return model.HadoopLogContent{}, locationErr
		}
		redirectURL := hadoopJobHistoryLogURL(ds, location)
		if !hadoopLogURLBelongsToSource(ds, base, redirectURL) {
			return model.HadoopLogContent{}, errors.New("重定向日志地址不属于当前 Hadoop 数据源")
		}
		requestURL = redirectURL
		resp, err = hadoopLogGet(client, redirectURL)
		if err != nil {
			return model.HadoopLogContent{}, fmt.Errorf("JobHistory 日志服务不可用：%w", err)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return model.HadoopLogContent{}, errors.New(resp.Status)
	}
	const maxContainerLogBytes = 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxContainerLogBytes+1))
	if err != nil {
		return model.HadoopLogContent{}, err
	}
	truncated := len(body) > maxContainerLogBytes
	if truncated {
		body = body[:maxContainerLogBytes]
	}
	content, err := readableHadoopLogFromDirectory(client, ds, base, requestURL, body)
	if err != nil {
		return model.HadoopLogContent{}, err
	}
	return model.HadoopLogContent{Content: content, Truncated: truncated}, nil
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
	// YARN can return a NodeManager's internal hostname. When this datasource
	// has an explicit NodeManager endpoint, only preserve the container-log path
	// and rewrite the request to that configured endpoint below.
	if strings.TrimSpace(hadoop.Options["nodeManagerUrl"]) != "" && strings.HasPrefix(endpoint.Path, "/node/containerlogs/") {
		return true
	}
	for _, configured := range []string{base, hadoop.Options["nodeManagerUrl"], hadoop.Options["jobHistoryUrl"]} {
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
	lines := make([]string, 0, len(blocks))
	for _, block := range blocks {
		value := strings.TrimSpace(html.UnescapeString(block[1]))
		if value != "" {
			lines = append(lines, value)
		}
	}
	if len(lines) == 0 {
		return text
	}
	return strings.Join(lines, "\n\n")
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
	for _, fileURL := range files {
		if !hadoopLogURLBelongsToSource(hadoop, base, fileURL) {
			continue
		}
		resp, getErr := hadoopLogGet(client, hadoopNodeManagerLogURL(hadoop, fileURL))
		if getErr != nil {
			continue
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			_ = resp.Body.Close()
			continue
		}
		content, readErr := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		_ = resp.Body.Close()
		if readErr != nil {
			continue
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
		sections = append(sections, "===== "+name+" =====\n"+value)
	}
	if len(sections) == 0 {
		return "容器日志目录存在，但暂未生成可读取的 stdout、stderr 或 syslog。", nil
	}
	return strings.Join(sections, "\n\n"), nil
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

func hadoopNodeManagerLogURL(hadoop model.DataSource, logURL string) string {
	configured := strings.TrimRight(strings.TrimSpace(hadoop.Options["nodeManagerUrl"]), "/")
	if configured == "" {
		return logURL
	}
	logEndpoint, err := url.Parse(logURL)
	if err != nil || !strings.HasPrefix(logEndpoint.Path, "/node/containerlogs/") {
		return logURL
	}
	resolved := configured + logEndpoint.EscapedPath()
	if logEndpoint.RawQuery != "" {
		resolved += "?" + logEndpoint.RawQuery
	}
	return resolved
}

func hadoopJobHistoryLogURL(hadoop model.DataSource, target *url.URL) string {
	configured := strings.TrimSpace(hadoop.Options["jobHistoryUrl"])
	if configured == "" || target == nil {
		if target == nil {
			return ""
		}
		return target.String()
	}
	base, err := url.Parse(configured)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return target.String()
	}
	base.Path = target.Path
	base.RawPath = target.RawPath
	base.RawQuery = target.RawQuery
	return base.String()
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
