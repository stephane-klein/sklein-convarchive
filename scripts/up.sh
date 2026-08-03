#!/usr/bin/env bash
set -e

cd "$(dirname "$0")/../"

podman compose up -d

echo "Waiting for RustFS S3 API to be ready..."
until curl -fsS http://localhost:9000/health >/dev/null 2>&1; do
  sleep 1
done

echo "RustFS is ready:"
echo "  S3 API:   http://localhost:9000"
echo "  Console:  http://localhost:9001 (login: rustfsadmin / rustfsadmin)"
echo "The bucket is created automatically on the first 'sklein-convarchive mattermost archive' run."
