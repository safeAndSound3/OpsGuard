#!/usr/bin/env bash
set -eu

MYSQL_HOST="${MYSQL_HOST:-mysql}"
MYSQL_PORT="${MYSQL_PORT:-3306}"
MYSQL_USER="${MYSQL_USER:-root}"
MYSQL_PASSWORD="${MYSQL_ROOT_PASSWORD:-Hh0321}"
MYSQL_DATABASE="${MYSQL_DATABASE:-opsguard_mock}"
MYSQL_MOCK_BATCH_SIZE="${MYSQL_MOCK_BATCH_SIZE:-8}"
MYSQL_MOCK_SLEEP_SECONDS="${MYSQL_MOCK_SLEEP_SECONDS:-10}"

mysql_exec() {
  mysql -h"${MYSQL_HOST}" -P"${MYSQL_PORT}" -u"${MYSQL_USER}" -p"${MYSQL_PASSWORD}" --default-character-set=utf8mb4 "$@"
}

until mysqladmin ping -h"${MYSQL_HOST}" -P"${MYSQL_PORT}" -u"${MYSQL_USER}" -p"${MYSQL_PASSWORD}" --silent; do
  sleep 2
done

mysql_exec <<SQL
CREATE DATABASE IF NOT EXISTS \`${MYSQL_DATABASE}\` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE \`${MYSQL_DATABASE}\`;
CREATE TABLE IF NOT EXISTS mock_orders (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  order_no VARCHAR(64) NOT NULL,
  user_id BIGINT NOT NULL,
  region VARCHAR(32) NOT NULL,
  status VARCHAR(24) NOT NULL,
  amount DECIMAL(12,2) NOT NULL,
  channel VARCHAR(24) NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  KEY idx_created_at (created_at),
  KEY idx_status_created (status, created_at),
  KEY idx_user_created (user_id, created_at)
);
CREATE TABLE IF NOT EXISTS mock_payments (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  order_no VARCHAR(64) NOT NULL,
  provider VARCHAR(24) NOT NULL,
  pay_status VARCHAR(24) NOT NULL,
  latency_ms INT NOT NULL,
  amount DECIMAL(12,2) NOT NULL,
  created_at DATETIME NOT NULL,
  KEY idx_created_at (created_at),
  KEY idx_provider_status (provider, pay_status)
);
CREATE TABLE IF NOT EXISTS mock_api_events (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  trace_id VARCHAR(64) NOT NULL,
  service_name VARCHAR(48) NOT NULL,
  endpoint VARCHAR(120) NOT NULL,
  http_status INT NOT NULL,
  latency_ms INT NOT NULL,
  created_at DATETIME NOT NULL,
  KEY idx_service_created (service_name, created_at),
  KEY idx_status_created (http_status, created_at)
);
CREATE TABLE IF NOT EXISTS mock_inventory_snapshots (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  sku VARCHAR(64) NOT NULL,
  warehouse VARCHAR(32) NOT NULL,
  available_qty INT NOT NULL,
  locked_qty INT NOT NULL,
  created_at DATETIME NOT NULL,
  KEY idx_sku_created (sku, created_at)
);
CREATE TABLE IF NOT EXISTS mock_app_metrics (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  metric_name VARCHAR(80) NOT NULL,
  metric_value DECIMAL(18,4) NOT NULL,
  tags_json JSON NULL,
  created_at DATETIME NOT NULL,
  KEY idx_metric_created (metric_name, created_at)
);
SQL

regions=(cn-north cn-east cn-south cn-west)
statuses=(created paid shipped finished cancelled refunding)
channels=(app web miniapp api)
providers=(wechat alipay unionpay applepay)
services=(order-service payment-service inventory-service user-service gateway)
endpoints=(/api/orders /api/orders/pay /api/inventory/lock /api/users/profile /api/cart/checkout /api/refunds)
warehouses=(beijing shanghai guangzhou chengdu)
metrics=(cpu_usage memory_usage queue_depth worker_busy db_pool_usage cache_hit_rate)

while true; do
  now="$(date '+%Y-%m-%d %H:%M:%S')"
  sql="USE \`${MYSQL_DATABASE}\`;"
  for i in $(seq 1 "${MYSQL_MOCK_BATCH_SIZE}"); do
    order_no="MOCK$(date +%Y%m%d%H%M%S)$RANDOM$i"
    user_id=$((100000 + RANDOM % 900000))
    amount_major=$((10 + RANDOM % 2000))
    amount_minor=$((RANDOM % 100))
    amount="${amount_major}.${amount_minor}"
    region="${regions[$((RANDOM % ${#regions[@]}))]}"
    status="${statuses[$((RANDOM % ${#statuses[@]}))]}"
    channel="${channels[$((RANDOM % ${#channels[@]}))]}"
    provider="${providers[$((RANDOM % ${#providers[@]}))]}"
    pay_status="success"
    if [ $((RANDOM % 20)) -eq 0 ]; then pay_status="failed"; fi
    latency=$((20 + RANDOM % 1800))
    service="${services[$((RANDOM % ${#services[@]}))]}"
    endpoint="${endpoints[$((RANDOM % ${#endpoints[@]}))]}"
    http_status=200
    if [ $((RANDOM % 25)) -eq 0 ]; then http_status=500; elif [ $((RANDOM % 12)) -eq 0 ]; then http_status=429; fi
    api_latency=$((5 + RANDOM % 2500))
    sku="SKU-$((1000 + RANDOM % 9000))"
    warehouse="${warehouses[$((RANDOM % ${#warehouses[@]}))]}"
    available=$((RANDOM % 5000))
    locked=$((RANDOM % 300))
    metric="${metrics[$((RANDOM % ${#metrics[@]}))]}"
    metric_value="$((RANDOM % 100)).$((RANDOM % 10000))"
    sql="${sql}
INSERT INTO mock_orders (order_no, user_id, region, status, amount, channel, created_at, updated_at)
VALUES ('${order_no}', ${user_id}, '${region}', '${status}', ${amount}, '${channel}', '${now}', '${now}');
INSERT INTO mock_payments (order_no, provider, pay_status, latency_ms, amount, created_at)
VALUES ('${order_no}', '${provider}', '${pay_status}', ${latency}, ${amount}, '${now}');
INSERT INTO mock_api_events (trace_id, service_name, endpoint, http_status, latency_ms, created_at)
VALUES (UUID(), '${service}', '${endpoint}', ${http_status}, ${api_latency}, '${now}');
INSERT INTO mock_inventory_snapshots (sku, warehouse, available_qty, locked_qty, created_at)
VALUES ('${sku}', '${warehouse}', ${available}, ${locked}, '${now}');
INSERT INTO mock_app_metrics (metric_name, metric_value, tags_json, created_at)
VALUES ('${metric}', ${metric_value}, JSON_OBJECT('region', '${region}', 'service', '${service}'), '${now}');"
  done
  sql="${sql}
UPDATE mock_orders SET status = 'finished', updated_at = '${now}' WHERE status = 'shipped' AND created_at < NOW() - INTERVAL 2 MINUTE LIMIT 20;
UPDATE mock_inventory_snapshots SET locked_qty = locked_qty + 1 WHERE available_qty > 1000 ORDER BY id DESC LIMIT 20;
SELECT COUNT(*), AVG(latency_ms), MAX(latency_ms) FROM mock_api_events WHERE created_at >= NOW() - INTERVAL 5 MINUTE GROUP BY service_name;"
  mysql_exec -e "${sql}" >/dev/null
  sleep "${MYSQL_MOCK_SLEEP_SECONDS}"
done
