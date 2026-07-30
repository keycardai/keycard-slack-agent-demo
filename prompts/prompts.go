package prompts

import _ "embed"

//go:embed system.md
var System string

//go:embed channel_mode.md
var ChannelMode string

//go:embed dm_mode.md
var DMMode string
