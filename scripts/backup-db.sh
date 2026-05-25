#!/bin/bash
set -e
BACKUP_DIR="/backups"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
FILENAME="proxima_backup_${TIMESTAMP}.sql.gz"

mkdir -p "$BACKUP_DIR"

pg_dump -U "${POSTGRES_USER}" "${POSTGRES_DB}" | gzip > "${BACKUP_DIR}/${FILENAME}"

# Keep only last 7 days
find "$BACKUP_DIR" -name "proxima_backup_*.sql.gz" -mtime +7 -delete

echo "Backup completed: ${FILENAME}"
