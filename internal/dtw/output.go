package dtw

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// OutputMode 控制输出格式
type OutputMode int

const (
	OutputHuman OutputMode = iota
	OutputJSON
)

// Printer 根据模式输出人类可读文本或 JSON
type Printer struct {
	Mode   OutputMode
	Writer io.Writer
}

// NewPrinter 创建 Printer，jsonMode=true 时输出 JSON
func NewPrinter(jsonMode bool) *Printer {
	mode := OutputHuman
	if jsonMode {
		mode = OutputJSON
	}
	return &Printer{Mode: mode, Writer: os.Stdout}
}

// PrintJSON 输出格式化的 JSON
func (p *Printer) PrintJSON(data any) error {
	enc := json.NewEncoder(p.Writer)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

// PrintHuman 输出人类可读文本
func (p *Printer) PrintHuman(format string, args ...any) {
	fmt.Fprintf(p.Writer, format+"\n", args...)
}

// Print 根据当前模式输出：JSON 模式输出 data，否则输出 humanText
func (p *Printer) Print(humanText string, data any) error {
	if p.Mode == OutputJSON {
		return p.PrintJSON(data)
	}
	p.PrintHuman("%s", humanText)
	return nil
}
