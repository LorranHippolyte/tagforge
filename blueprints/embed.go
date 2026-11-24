package blueprintspecs

import "embed"

// FS embeds all official declarative YAML blueprints into the tagforge binary.
//
//go:embed *.yaml
var FS embed.FS
