package assets

import (
	"embed"
	_ "embed"
)

//go:embed openapi.yaml
var OpenApiData []byte

//go:embed menus.yaml
var MenusData []byte

//go:embed descriptor.bin
var DescriptorData []byte

//go:embed DejaVuSans.ttf
var DejaVuSansFont []byte

//go:embed DejaVuSans-Italic.ttf
var DejaVuSansItalicFont []byte

//go:embed GreatVibes-Regular.ttf
var GreatVibesFont []byte

//go:embed all:frontend-dist
var FrontendDist embed.FS
