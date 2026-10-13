package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type MySQLAsyncJob struct {
	ID         string          `json:"id"`
	SourceID   string          `json:"sourceId"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	Progress   int             `json:"progress"`
	Message    string          `json:"message"`
	Result     json.RawMessage `json:"result,omitempty"`
	FileName   string          `json:"fileName,omitempty"`
	CreatedAt  string          `json:"createdAt"`
	StartedAt  string          `json:"startedAt,omitempty"`
	FinishedAt string          `json:"finishedAt,omitempty"`
}

type mysqlJobRequest struct {
	Database   string `json:"database"`
	Table      string `json:"table"`
	Scope      string `json:"scope"`
	Format     string `json:"format"`
	SQL        string `json:"sql"`
	Confirmed  bool   `json:"confirmed"`
	Username   string `json:"username"`
	UploadName string `json:"uploadName"`
}

var mysqlJobCancels sync.Map

type mysqlJobWork struct {
	job     MySQLAsyncJob
	request mysqlJobRequest
}

var mysqlJobQueue = make(chan mysqlJobWork, 20)
var mysqlJobWorkersOnce sync.Once

func startMySQLJobWorkers() {
	mysqlJobWorkersOnce.Do(func() {
		for i := 0; i < 2; i++ {
			go func() {
				for work := range mysqlJobQueue {
					runMySQLAsyncJob(work.job, work.request)
				}
			}()
		}
	})
}

func initMySQLAsyncJobStore(current *sql.DB) error {
	startMySQLJobWorkers()
	_, err := current.Exec(`CREATE TABLE IF NOT EXISTS mysql_async_jobs (
		id varchar(64) PRIMARY KEY, source_id varchar(64) NOT NULL, kind varchar(16) NOT NULL,
		status varchar(16) NOT NULL, progress int NOT NULL DEFAULT 0, message varchar(512) NOT NULL DEFAULT '',
		request_json mediumtext NOT NULL, result_json mediumtext NOT NULL, file_name varchar(255) NOT NULL DEFAULT '',
		file_content longblob NULL, cancel_requested tinyint(1) NOT NULL DEFAULT 0,
		created_at datetime NOT NULL, started_at datetime NULL, finished_at datetime NULL,
		INDEX idx_mysql_async_jobs_source (source_id, created_at))`)
	if err != nil {
		return err
	}
	_, _ = current.Exec(`UPDATE mysql_async_jobs SET status='failed', message='平台重启，任务已中断', finished_at=NOW() WHERE status IN ('queued','running')`)
	return nil
}

func createMySQLAsyncJob(sourceID, kind string, request mysqlJobRequest) (MySQLAsyncJob, error) {
	if currentStore() == nil {
		return MySQLAsyncJob{}, errors.New("平台数据库不可用")
	}
	if ds, err := GetDataSourceByID(sourceID); err != nil || !strings.EqualFold(ds.Type, "mysql") || !ds.Enabled {
		return MySQLAsyncJob{}, errors.New("MySQL 数据源不可用")
	}
	id, err := newMySQLJobID()
	if err != nil {
		return MySQLAsyncJob{}, err
	}
	// Jobs do not resume after a platform restart, so keep only a redacted
	// statement in the database. The worker receives the original request from
	// memory and the audit history records the same redacted representation.
	persistedRequest := request
	if kind == "query" {
		persistedRequest.SQL = redactSQLForHistory(request.SQL)
	}
	raw, _ := json.Marshal(persistedRequest)
	now := time.Now()
	_, err = currentStore().Exec(`INSERT INTO mysql_async_jobs (id, source_id, kind, status, progress, message, request_json, result_json, file_name, created_at) VALUES (?, ?, ?, 'queued', 0, '等待执行', ?, '{}', ?, ?)`, id, sourceID, kind, string(raw), request.UploadName, now)
	if err != nil {
		return MySQLAsyncJob{}, err
	}
	job := MySQLAsyncJob{ID: id, SourceID: sourceID, Kind: kind, Status: "queued", Message: "等待执行", FileName: request.UploadName, CreatedAt: now.Format(time.RFC3339)}
	select {
	case mysqlJobQueue <- mysqlJobWork{job: job, request: request}:
	default:
		_, _ = currentStore().Exec(`UPDATE mysql_async_jobs SET status='failed', message='任务队列已满，请稍后重试', finished_at=NOW() WHERE id=?`, job.ID)
		return MySQLAsyncJob{}, errors.New("MySQL 后台任务队列已满，请稍后重试")
	}
	return job, nil
}

func StartMySQLExportJob(sourceID string, options MySQLTransferOptions) (MySQLAsyncJob, error) {
	return createMySQLAsyncJob(sourceID, "export", mysqlJobRequest{Database: options.Database, Table: options.Table, Scope: options.Scope, Format: options.Format})
}
func StartMySQLQueryJob(sourceID, database, statement, username string, confirmed bool) (MySQLAsyncJob, error) {
	if strings.TrimSpace(statement) == "" {
		return MySQLAsyncJob{}, errors.New("SQL 不能为空")
	}
	if !confirmed && !GetOperationConfirmations() {
		confirmed = true
	}
	return createMySQLAsyncJob(sourceID, "query", mysqlJobRequest{Database: database, SQL: statement, Username: username, Confirmed: confirmed, Format: "csv"})
}
func StartMySQLImportJob(sourceID, database, table, format, uploadPath, uploadName string, confirmed bool) (MySQLAsyncJob, error) {
	if GetOperationConfirmations() && !confirmed {
		return MySQLAsyncJob{}, errMySQLWriteConfirmation
	}
	return createMySQLAsyncJob(sourceID, "import", mysqlJobRequest{Database: database, Table: table, Format: format, SQL: uploadPath, UploadName: filepath.Base(strings.TrimSpace(uploadName)), Confirmed: confirmed})
}

func runMySQLAsyncJob(job MySQLAsyncJob, request mysqlJobRequest) {
	started := time.Now()
	var cancelled int
	if err := currentStore().QueryRow(`SELECT cancel_requested FROM mysql_async_jobs WHERE id=?`, job.ID).Scan(&cancelled); err == nil && cancelled != 0 {
		setMySQLJobState(job.ID, "cancelled", 0, "任务已取消", nil, "", nil)
		if job.Kind == "import" {
			_ = os.Remove(request.SQL)
		}
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx, timeoutCancel := context.WithTimeout(ctx, 30*time.Minute)
	defer timeoutCancel()
	mysqlJobCancels.Store(job.ID, cancel)
	defer func() { cancel(); mysqlJobCancels.Delete(job.ID) }()
	setMySQLJobState(job.ID, "running", 5, "正在执行", nil, "", nil)
	var result any
	var filename string
	var content []byte
	var err error
	switch job.Kind {
	case "export":
		setMySQLJobState(job.ID, "running", 20, "正在读取库表结构和数据", nil, "", nil)
		content, filename, err = exportMySQLDataContext(ctx, job.SourceID, MySQLTransferOptions{Database: request.Database, Table: request.Table, Scope: request.Scope, Format: request.Format})
		if err == nil {
			result = map[string]any{"message": "导出完成"}
		}
	case "import":
		defer os.Remove(request.SQL)
		content, err = os.ReadFile(request.SQL)
		if err == nil {
			result, err = importMySQLDataContext(ctx, job.SourceID, request.Database, request.Table, request.Format, content, func(p int, m string) { setMySQLJobState(job.ID, "running", p, m, nil, "", nil) })
		}
	case "query":
		setMySQLJobState(job.ID, "running", 20, "正在执行 SQL", nil, "", nil)
		result, err = executeMySQLStatementContext(ctx, job.SourceID, request.Database, request.SQL, request.Confirmed)
		statementResult, _ := result.(MySQLStatementResult)
		if !errors.Is(err, errMySQLWriteConfirmation) {
			recordMySQLSQLHistory(job.SourceID, strings.TrimSpace(request.Database), redactSQLForHistory(request.SQL), strings.TrimSpace(request.Username), statementResult, err, time.Since(started))
		}
		if err == nil {
			if statementResult, ok := result.(MySQLStatementResult); ok && len(statementResult.Columns) > 0 {
				var buffer bytes.Buffer
				writer := csv.NewWriter(&buffer)
				_ = writer.Write(statementResult.Columns)
				for _, row := range statementResult.Rows {
					values := make([]string, len(row))
					for index, value := range row {
						values[index] = mysqlTransferString(value)
					}
					_ = writer.Write(values)
				}
				writer.Flush()
				content = append([]byte{0xEF, 0xBB, 0xBF}, buffer.Bytes()...)
				filename = "sql-query-result.csv"
			}
		}
	}
	if errors.Is(err, context.Canceled) {
		setMySQLJobState(job.ID, "cancelled", 0, "任务已取消", nil, "", nil)
		return
	}
	if err != nil {
		var failedResult []byte
		if result != nil {
			failedResult, _ = json.Marshal(result)
		}
		setMySQLJobState(job.ID, "failed", 0, err.Error(), failedResult, "", nil)
		return
	}
	raw, _ := json.Marshal(result)
	setMySQLJobState(job.ID, "succeeded", 100, "执行完成", raw, filename, content)
}

func setMySQLJobState(id, status string, progress int, message string, result []byte, filename string, content []byte) {
	current := currentStore()
	if current == nil {
		return
	}
	if len(message) > 512 {
		message = message[:512]
	}
	if status == "running" {
		_, _ = current.Exec(`UPDATE mysql_async_jobs SET status=?,progress=?,message=?,started_at=COALESCE(started_at,NOW()) WHERE id=?`, status, progress, message, id)
		return
	}
	if status == "succeeded" {
		_, _ = current.Exec(`UPDATE mysql_async_jobs SET status=?,progress=?,message=?,result_json=?,file_name=CASE WHEN ?='' THEN file_name ELSE ? END,file_content=?,finished_at=NOW() WHERE id=?`, status, progress, message, string(result), filename, filename, content, id)
		return
	}
	if status == "failed" && len(result) > 0 {
		_, _ = current.Exec(`UPDATE mysql_async_jobs SET status=?,progress=?,message=?,result_json=?,finished_at=NOW() WHERE id=?`, status, progress, message, string(result), id)
		return
	}
	_, _ = current.Exec(`UPDATE mysql_async_jobs SET status=?,message=?,finished_at=NOW() WHERE id=?`, status, message, id)
}

func GetMySQLAsyncJob(id string) (MySQLAsyncJob, error) {
	var job MySQLAsyncJob
	var result string
	var created time.Time
	var started, finished sql.NullTime
	err := currentStore().QueryRow(`SELECT id,source_id,kind,status,progress,message,result_json,file_name,created_at,started_at,finished_at FROM mysql_async_jobs WHERE id=?`, id).Scan(&job.ID, &job.SourceID, &job.Kind, &job.Status, &job.Progress, &job.Message, &result, &job.FileName, &created, &started, &finished)
	if err != nil {
		return job, err
	}
	job.Result = json.RawMessage(result)
	job.CreatedAt = created.Format(time.RFC3339)
	if started.Valid {
		job.StartedAt = started.Time.Format(time.RFC3339)
	}
	if finished.Valid {
		job.FinishedAt = finished.Time.Format(time.RFC3339)
	}
	return job, nil
}
func ListMySQLAsyncJobs(sourceID string) []MySQLAsyncJob {
	rows, err := currentStore().Query(`SELECT id FROM mysql_async_jobs WHERE source_id=? ORDER BY created_at DESC LIMIT 20`, sourceID)
	if err != nil {
		return []MySQLAsyncJob{}
	}
	defer rows.Close()
	jobs := []MySQLAsyncJob{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			if job, e := GetMySQLAsyncJob(id); e == nil {
				jobs = append(jobs, job)
			}
		}
	}
	return jobs
}
func CancelMySQLAsyncJob(id string) error {
	job, err := GetMySQLAsyncJob(id)
	if err != nil {
		return err
	}
	if job.Status != "queued" && job.Status != "running" {
		return errors.New("任务当前不可取消")
	}
	if cancel, ok := mysqlJobCancels.Load(id); ok {
		cancel.(context.CancelFunc)()
	}
	_, err = currentStore().Exec(`UPDATE mysql_async_jobs SET cancel_requested=1,message='正在取消' WHERE id=?`, id)
	return err
}
func ReadMySQLAsyncJobFile(id string) ([]byte, string, error) {
	var name string
	var content []byte
	err := currentStore().QueryRow(`SELECT file_name,file_content FROM mysql_async_jobs WHERE id=? AND status='succeeded'`, id).Scan(&name, &content)
	if err != nil {
		return nil, "", err
	}
	if name == "" || len(content) == 0 {
		return nil, "", errors.New("该任务没有可下载文件")
	}
	return content, name, nil
}
func SaveMySQLJobUpload(fileName string, content []byte) (string, error) {
	if len(content) == 0 || len(content) > maxMySQLTransferBytes {
		return "", errors.New("导入文件必须在 20 MB 以内")
	}
	dir := filepath.Join(os.TempDir(), "opsguard-mysql-jobs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	id, err := newMySQLJobID()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+".upload")
	if err := os.WriteFile(path, content, 0600); err != nil {
		return "", err
	}
	return path, nil
}
func newMySQLJobID() (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate job id: %w", err)
	}
	return "mysql_" + hex.EncodeToString(raw), nil
}
