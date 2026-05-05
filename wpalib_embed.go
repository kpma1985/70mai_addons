//go:build !firmware

package main

import "embed"

//go:embed bin/wpalib/*
var wpalibFS embed.FS
