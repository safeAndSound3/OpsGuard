package service

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"monitor-platform/internal/model"
)

func middlewareAddress(ds model.DataSource) string {
	return net.JoinHostPort(strings.TrimSpace(ds.Host), strings.TrimSpace(ds.Port))
}

// TestRedisDataSource verifies the service with the native RESP PING command.
func TestRedisDataSource(ds model.DataSource) error {
	if _, err := collectRedisMetrics(ds); err == nil {
		return nil
	}
	connection, err := net.DialTimeout("tcp", middlewareAddress(ds), 5*time.Second)
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))

	writer := bufio.NewWriter(connection)
	if password := strings.TrimSpace(ds.Password); password != "" {
		username := strings.TrimSpace(ds.Username)
		if username == "" {
			_, _ = fmt.Fprintf(writer, "*2\r\n$4\r\nAUTH\r\n$%d\r\n%s\r\n", len(password), password)
		} else {
			_, _ = fmt.Fprintf(writer, "*3\r\n$4\r\nAUTH\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n", len(username), username, len(password), password)
		}
		if err := writer.Flush(); err != nil {
			return err
		}
		response, readErr := bufio.NewReader(connection).ReadString('\n')
		if readErr != nil || !strings.HasPrefix(response, "+OK") {
			if readErr != nil {
				return readErr
			}
			return fmt.Errorf("Redis authentication failed: %s", strings.TrimSpace(response))
		}
	}
	_, _ = writer.WriteString("*1\r\n$4\r\nPING\r\n")
	if err := writer.Flush(); err != nil {
		return err
	}
	response, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(response, "+PONG") {
		return fmt.Errorf("unexpected Redis PING response: %s", strings.TrimSpace(response))
	}
	return nil
}

// TestClickHouseDataSource executes a read-only SELECT 1 through the HTTP API.
func TestClickHouseDataSource(ds model.DataSource) error {
	if _, err := collectClickHouseMetrics(ds); err == nil {
		return nil
	}
	endpoint := "http://" + middlewareAddress(ds) + "/?query=" + url.QueryEscape("SELECT 1")
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(ds.Username) != "" {
		req.SetBasicAuth(ds.Username, ds.Password)
	}
	client := &http.Client{Timeout: 8 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("ClickHouse returned HTTP %d", response.StatusCode)
	}
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(response.Body)
	if strings.TrimSpace(body.String()) != "1" {
		return fmt.Errorf("unexpected ClickHouse response: %s", strings.TrimSpace(body.String()))
	}
	return nil
}

// TestKafkaDataSource intentionally stays protocol-version agnostic. It checks
// that the configured bootstrap broker accepts TCP connections; detailed topic
// and consumer metrics remain the responsibility of Prometheus exporters.
func TestKafkaDataSource(ds model.DataSource) error {
	if _, err := collectKafkaMetrics(ds); err == nil {
		return nil
	}
	connection, err := net.DialTimeout("tcp", middlewareAddress(ds), 5*time.Second)
	if err != nil {
		return err
	}
	return connection.Close()
}
