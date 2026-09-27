#!/usr/bin/env bash

set -euo pipefail

project_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$project_dir"

if ! command -v docker >/dev/null 2>&1; then
  echo "Ошибка: Docker не найден. Установите Docker и повторите запуск." >&2
  exit 1
fi

if ! docker compose version >/dev/null 2>&1; then
  echo "Ошибка: команда 'docker compose' недоступна." >&2
  exit 1
fi

env_file="$project_dir/.env"
key_name=IDEMPOTENCY_ENCRYPTION_KEY
key=""

if [[ -f "$env_file" ]]; then
  key=$(awk -F= -v name="$key_name" '$1 == name { print substr($0, length(name) + 2) }' "$env_file" | tail -n 1)
fi

if [[ -z "$key" ]]; then
  if ! command -v openssl >/dev/null 2>&1; then
    echo "Ошибка: OpenSSL не найден; он нужен для создания локального ключа." >&2
    exit 1
  fi

  key=$(openssl rand -hex 32)
  umask 077
  if [[ -f "$env_file" && -s "$env_file" ]]; then
    printf '\n%s=%s\n' "$key_name" "$key" >>"$env_file"
  else
    printf '%s=%s\n' "$key_name" "$key" >"$env_file"
  fi
  echo "Создан локальный секрет в .env."
fi

if [[ ! "$key" =~ ^[[:xdigit:]]{64}$ ]]; then
  echo "Ошибка: $key_name в .env должен содержать ровно 64 hex-символа." >&2
  exit 1
fi

chmod 600 "$env_file"

echo "nullchat запускается на http://127.0.0.1:8080 (остановка: Ctrl+C)"
exec docker compose up --build
