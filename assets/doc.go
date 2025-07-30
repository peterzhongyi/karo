package assets

import "embed"

//go:embed v1/**/*
var Embedded embed.FS
