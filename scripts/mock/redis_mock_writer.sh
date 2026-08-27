#!/usr/bin/env sh
set -eu

REDIS_HOST="${REDIS_HOST:-redis}"
REDIS_PORT="${REDIS_PORT:-6379}"
REDIS_MOCK_BATCH_SIZE="${REDIS_MOCK_BATCH_SIZE:-20}"
REDIS_MOCK_SLEEP_SECONDS="${REDIS_MOCK_SLEEP_SECONDS:-5}"

until redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" ping >/dev/null 2>&1; do
  sleep 1
done

redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" CONFIG SET slowlog-log-slower-than 0 >/dev/null || true
redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" CONFIG SET slowlog-max-len 256 >/dev/null || true

seed=$$
next_rand() {
  seed=$(((seed * 1103515245 + 12345) % 2147483647))
  rand="$seed"
}

while true; do
  ts="$(date +%s)"
  i=0
  while [ "$i" -lt "${REDIS_MOCK_BATCH_SIZE}" ]; do
    next_rand; r1="$rand"
    next_rand; r2="$rand"
    next_rand; r3="$rand"
    next_rand; r4="$rand"
    user_id=$((100000 + r1 % 900000))
    order_id=$((10000000 + r2 % 90000000))
    sku=$((1000 + r3 % 9000))
    region_index=$((r4 % 4))
    case "$region_index" in
      0) region="cn-north" ;;
      1) region="cn-east" ;;
      2) region="cn-south" ;;
      *) region="cn-west" ;;
    esac
    redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" SETEX "session:${user_id}" 1800 "user=${user_id};region=${region};ts=${ts}" >/dev/null
    next_rand; r5="$rand"
    next_rand; r6="$rand"
    redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" HSET "order:${order_id}" user_id "${user_id}" status "paid" amount "$((r5 % 2000)).$((r6 % 100))" updated_at "${ts}" >/dev/null
    redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" EXPIRE "order:${order_id}" 3600 >/dev/null
    redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" LPUSH "queue:payment:pending" "${order_id}" >/dev/null
    redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" LTRIM "queue:payment:pending" 0 2000 >/dev/null
    next_rand; r7="$rand"
    next_rand; r8="$rand"
    next_rand; r9="$rand"
    redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" ZADD "ranking:sku:hot" "$((r7 % 100000))" "SKU-${sku}" >/dev/null
    redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" INCRBY "counter:api:${region}:${ts}" "$((1 + r8 % 30))" >/dev/null
    redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" EXPIRE "counter:api:${region}:${ts}" 900 >/dev/null
    redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" XADD "stream:ops:events" MAXLEN "~" 5000 "*" region "${region}" user_id "${user_id}" order_id "${order_id}" latency_ms "$((5 + r9 % 2500))" >/dev/null
    redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" GET "session:${user_id}" >/dev/null
    i=$((i + 1))
  done
  redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" DBSIZE >/dev/null
  redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" SLOWLOG LEN >/dev/null
  sleep "${REDIS_MOCK_SLEEP_SECONDS}"
done
