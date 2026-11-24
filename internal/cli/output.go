package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Format controls CLI stdout formatting.
type Format string

const (
	FormatTable    Format = "table"
	FormatJSON     Format = "json"
	FormatMarkdown Format = "markdown"
)

// ParseFormat validates the --format flag value.
func ParseFormat(raw string) (Format, error) {
	switch Format(strings.ToLower(strings.TrimSpace(raw))) {
	case "", FormatTable:
		return FormatTable, nil
	case FormatJSON:
		return FormatJSON, nil
	case FormatMarkdown:
		return FormatMarkdown, nil
	default:
		return "", fmt.Errorf("invalid format %q: must be 'table', 'json', or 'markdown'", raw)
	}
}

// Printer writes structured command results strictly to Out (never mixing logs into stdout).
type Printer struct {
	Out    io.Writer
	Err    io.Writer
	Format Format
}

// PrintJSON writes v formatted as indented JSON to Out.
func (p *Printer) PrintJSON(v any) error {
	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// PrintTable writes headers and rows cleanly aligned with tabwriter, or falls back to JSON
// when Format == FormatJSON.
func (p *Printer) PrintTable(jsonVal any, headers []string, rows [][]string) error {
	if p.Format == FormatJSON {
		return p.PrintJSON(jsonVal)
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintln(p.Out, "No items found.")
		return err
	}
	tw := tabwriter.NewWriter(p.Out, 0, 4, 2, ' ', 0)
	if len(headers) > 0 {
		if _, err := fmt.Fprintln(tw, strings.Join(headers, "\t")); err != nil {
			return err
		}
		seps := make([]string, len(headers))
		for i, h := range headers {
			seps[i] = strings.Repeat("-", len(h))
		}
		if _, err := fmt.Fprintln(tw, strings.Join(seps, "\t")); err != nil {
			return err
		}
	}
	for _, row := range rows {
		if _, err := fmt.Fprintln(tw, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// PrintMessage prints a plain human-readable line in table mode, or JSON in json mode.
func (p *Printer) PrintMessage(jsonVal any, msg string) error {
	if p.Format == FormatJSON {
		return p.PrintJSON(jsonVal)
	}
	_, err := fmt.Fprintln(p.Out, msg)
	return err
}

// PrintMarkdown prints Markdown directly unless JSON format was explicitly requested.
func (p *Printer) PrintMarkdown(jsonVal any, md string) error {
	if p.Format == FormatJSON {
		return p.PrintJSON(jsonVal)
	}
	_, err := fmt.Fprint(p.Out, md)
	return err
}
