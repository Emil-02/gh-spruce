package card

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"text/template"

	"github.com/Emil-02/gh-spruce/internal/checks"
)

// textWidth approximates the width of Verdana 11px text, like shields.io.
func textWidth(s string) float64 {
	w := 0.0
	for _, r := range s {
		switch {
		case strings.ContainsRune("il.:|!'", r):
			w += 3.5
		case strings.ContainsRune("fjrt-() ", r):
			w += 4.5
		case r >= 'A' && r <= 'Z', strings.ContainsRune("mwMW", r):
			w += 8.5
		case r >= '0' && r <= '9':
			w += 7
		default:
			w += 6.6
		}
	}
	return math.Ceil(w)
}

// BadgeColor follows the shields.io palette.
func BadgeColor(score int) string {
	switch {
	case score >= 85:
		return "#4c1"
	case score >= 70:
		return "#97ca00"
	case score >= 55:
		return "#dfb317"
	case score >= 40:
		return "#fe7d37"
	}
	return "#e05d44"
}

const badgeSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="{{.W}}" height="20" role="img" aria-label="{{.Label}}: {{.Value}}">
  <title>{{.Label}}: {{.Value}}</title>
  <linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>
  <clipPath id="r"><rect width="{{.W}}" height="20" rx="3" fill="#fff"/></clipPath>
  <g clip-path="url(#r)">
    <rect width="{{.LW}}" height="20" fill="#555"/>
    <rect x="{{.LW}}" width="{{.VW}}" height="20" fill="{{.Color}}"/>
    <rect width="{{.W}}" height="20" fill="url(#s)"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">
    <text x="{{.LX}}" y="15" fill="#010101" fill-opacity=".3">{{.Label}}</text>
    <text x="{{.LX}}" y="14">{{.Label}}</text>
    <text x="{{.VX}}" y="15" fill="#010101" fill-opacity=".3">{{.Value}}</text>
    <text x="{{.VX}}" y="14">{{.Value}}</text>
  </g>
</svg>
`

var badgeTmpl = template.Must(template.New("badge").Parse(badgeSVG))

// Badge renders a shields-style "label | value" badge.
func Badge(label, value, color string) string {
	lw, vw := textWidth(label)+12, textWidth(value)+12
	d := map[string]any{
		"Label": xmlEscape(label), "Value": xmlEscape(value), "Color": color,
		"LW": lw, "VW": vw, "W": lw + vw, "LX": lw / 2, "VX": lw + vw/2,
	}
	var b bytes.Buffer
	_ = badgeTmpl.Execute(&b, d)
	return b.String()
}

// ScoreBadge renders "label | A 92".
func ScoreBadge(label string, score int, grade string) string {
	return Badge(label, fmt.Sprintf("%s %d", grade, score), BadgeColor(score))
}

const compactSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="300" height="100" viewBox="0 0 300 100" role="img" aria-labelledby="t">
  <title id="t">{{.Login}} repository health: {{.Score}}/100 ({{.Grade}})</title>
  <style>
    .h{font:600 16px 'Segoe UI',Ubuntu,'Helvetica Neue',Sans-Serif;fill:{{.Theme.Title}}}
    .l{font:400 13px 'Segoe UI',Ubuntu,'Helvetica Neue',Sans-Serif;fill:{{.Theme.Text}}}
    .g{font:800 20px 'Segoe UI',Ubuntu,'Helvetica Neue',Sans-Serif;fill:{{.Ring}}}
    .s{font:400 11px 'Segoe UI',Ubuntu,'Helvetica Neue',Sans-Serif;fill:{{.Theme.Muted}}}
  </style>
  <rect x="0.5" y="0.5" rx="6" width="299" height="99" fill="{{.Theme.Background}}" stroke="{{.Theme.Border}}"/>
  <g transform="translate(50 50)">
    <circle r="30" fill="none" stroke="{{.Theme.Track}}" stroke-width="6"/>
    <circle r="30" fill="none" stroke="{{.Ring}}" stroke-width="6" stroke-linecap="round" stroke-dasharray="{{printf "%.2f" .Circ}}" stroke-dashoffset="{{printf "%.2f" .Offset}}" transform="rotate(-90)"/>
    <text y="7" class="g" text-anchor="middle">{{.Grade}}</text>
  </g>
  <text x="96" y="40" class="h">{{.Login}}</text>
  <text x="96" y="62" class="l">Repo health {{.Score}}/100</text>
  <text x="96" y="82" class="s">{{.Public}} public repos · gh-spruce</text>
</svg>
`

var compactTmpl = template.Must(template.New("compact").Parse(compactSVG))

// RenderCompact renders a 300×100 card.
func RenderCompact(rep *checks.Report, themeName string) (string, error) {
	th, ok := Themes[themeName]
	if !ok {
		return "", fmt.Errorf("unknown theme %q (available: %s)", themeName, strings.Join(ThemeNames(), ", "))
	}
	public := 0
	for _, rr := range rep.Repos {
		if !rr.Repo.Private {
			public++
		}
	}
	circ := 2 * math.Pi * 30
	d := map[string]any{
		"Theme": th, "Login": xmlEscape(rep.Profile.Login), "Score": rep.Score, "Grade": rep.Grade, "Public": public,
		"Ring": ringColor(rep.Score), "Circ": circ, "Offset": circ * (1 - float64(rep.Score)/100),
		"Updated": rep.GeneratedAt.Format("2006-01-02"),
	}
	var b bytes.Buffer
	if err := compactTmpl.Execute(&b, d); err != nil {
		return "", err
	}
	return b.String(), nil
}

const errorSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="495" height="100" viewBox="0 0 495 100" role="img" aria-labelledby="t">
  <title id="t">gh-spruce: {{.Message}}</title>
  <style>
    .h{font:600 16px 'Segoe UI',Ubuntu,'Helvetica Neue',Sans-Serif;fill:#f85149}
    .l{font:400 13px 'Segoe UI',Ubuntu,'Helvetica Neue',Sans-Serif;fill:{{.Theme.Text}}}
  </style>
  <rect x="0.5" y="0.5" rx="6" width="494" height="99" fill="{{.Theme.Background}}" stroke="{{.Theme.Border}}"/>
  <text x="25" y="42" class="h">gh-spruce could not render this card</text>
  <text x="25" y="68" class="l">{{.Message}}</text>
</svg>
`

var errorTmpl = template.Must(template.New("error").Parse(errorSVG))

// ErrorCard renders a readable error instead of a broken image.
func ErrorCard(message, themeName string) string {
	th, ok := Themes[themeName]
	if !ok {
		th = Themes["dark"]
	}
	if len(message) > 70 {
		message = message[:69] + "…"
	}
	var b bytes.Buffer
	_ = errorTmpl.Execute(&b, map[string]any{"Theme": th, "Message": xmlEscape(message)})
	return b.String()
}
