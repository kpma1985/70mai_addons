//go:build firmware

package main

import "embed"

// Firmware builds keep the Addon Installer small enough for the RAMDisk.
// The host-side installer still embeds wpalib and can upload it to /mnt/sd.
var wpalibFS embed.FS
