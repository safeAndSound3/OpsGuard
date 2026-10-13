package service

import (
	"fmt"
	"net"
	"strings"
	"time"

	"monitor-platform/internal/model"
)

// HiveServer2 speaks a binary Thrift protocol. Until the Hive workspace adds
// a native driver, connection testing verifies the configured endpoint is
// reachable without pretending that a TCP handshake is a login.
func TestHiveDataSource(ds model.DataSource) error {
	host := strings.TrimSpace(ds.Host)
	port := strings.TrimSpace(ds.Port)
	if host == "" || port == "" {
		return fmt.Errorf("HiveServer2 地址和端口不能为空")
	}
	if err := validateOutboundAddress(host); err != nil {
		return err
	}
	connection, err := safeDialTimeout("tcp", net.JoinHostPort(host, port), 5*time.Second)
	if err != nil {
		return fmt.Errorf("HiveServer2 端口连接失败：%w", err)
	}
	return connection.Close()
}
