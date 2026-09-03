package service

import (
	"encoding/json"
	"errors"
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

var hadoopLogPreBlocks = regexp.MustCompile(`(?is)<pre[^>]*>(.*?)</pre>`)

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
	if err := hadoopGetJSON(base+"/ws/v1/cluster/apps", &payload); err != nil {
		return model.HadoopApplicationPage{}, err
	}
	applications := make(map[string]model.HadoopApplication, len(payload.Apps.App))
	for _, app := range payload.Apps.App {
		applications[app.ID] = app
	}
	for _, app := range listHadoopHistoryApplications(ds) {
		if _, exists := applications[app.ID]; !exists {
			applications[app.ID] = app
		}
	}
	all := make([]model.HadoopApplication, 0, len(applications))
	for _, app := range applications {
		all = append(all, app)
	}
	sort.Slice(all, func(i, j int) bool {
		leftActive, rightActive := hadoopApplicationActive(all[i]), hadoopApplicationActive(all[j])
		if leftActive != rightActive {
			return leftActive
		}
		leftTime, rightTime := all[i].FinishedTime, all[j].FinishedTime
		if leftTime == 0 {
			leftTime = all[i].StartedTime
		}
		if rightTime == 0 {
			rightTime = all[j].StartedTime
		}
		return leftTime > rightTime
	})
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
	if err := hadoopGetJSON(base+"/ws/v1/history/mapreduce/jobs", &payload); err != nil {
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
		return nil, err
	}
	if len(attempts.AppAttempts.AppAttempt) == 0 {
		return []model.HadoopContainer{}, nil
	}
	var payload struct {
		Containers struct {
			Container []model.HadoopContainer `json:"container"`
		} `json:"containers"`
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
		return nil, err
	}
	if len(payload.Containers.Container) == 0 && latest.ContainerID != "" {
		return []model.HadoopContainer{{
			ID:              latest.ContainerID,
			NodeHTTPAddress: latest.NodeHTTPAddress,
			State:           "COMPLETED",
			LogURL:          latest.LogsLink,
		}}, nil
	}
	return payload.Containers.Container, nil
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
	requestURL := hadoopNodeManagerLogURL(ds, logURL)
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(requestURL)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
		location, locationErr := resp.Location()
		_ = resp.Body.Close()
		if locationErr != nil {
			return "", locationErr
		}
		redirectURL := hadoopJobHistoryLogURL(ds, location)
		if !hadoopLogURLBelongsToSource(ds, base, redirectURL) {
			return "", errors.New("重定向日志地址不属于当前 Hadoop 数据源")
		}
		resp, err = client.Get(redirectURL)
		if err != nil {
			return "", err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", errors.New(resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return "", err
	}
	return readableHadoopLog(body), nil
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
	client := &http.Client{Timeout: 10 * time.Second}
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
