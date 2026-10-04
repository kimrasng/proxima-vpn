package database

const deviceBandwidthSchema = `
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS shaping_mode TEXT NOT NULL DEFAULT '';
`
