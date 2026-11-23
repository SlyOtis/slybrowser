package slybrowser

import (
	"embed"
	_ "embed"
)

//go:embed skills/slyb/SKILL.md
var Skill string

//go:embed browser
var Browser embed.FS
