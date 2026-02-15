package pomo

import (
	"embed"
)

//go:embed assets/break_bell.frames
//go:embed assets/resume_chime.frames
var audioAssets embed.FS

// Sound identifiers
const (
	SoundBreakBell   = "assets/break_bell.frames"
	SoundResumeChime = "assets/resume_chime.frames"
)
