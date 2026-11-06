package web

import (
	"embed"
	"errors"
)

//go:embed src/assets/fonts/consolas.woff src/assets/fonts/lxgwzk.woff2
var loginFonts embed.FS

// LoginFont serves only the two local console fonts, without exposing source files.
func LoginFont(name string) ([]byte, string, error) {
	switch name {
	case "consolas.woff":
		data, err := loginFonts.ReadFile("src/assets/fonts/" + name)
		return data, "font/woff", err
	case "lxgwzk.woff2":
		data, err := loginFonts.ReadFile("src/assets/fonts/" + name)
		return data, "font/woff2", err
	default:
		return nil, "", errors.New("字体不存在")
	}
}
