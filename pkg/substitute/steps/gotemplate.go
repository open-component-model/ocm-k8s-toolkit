package steps

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

type goTemplateSubstitutionStep struct {
	funcs template.FuncMap
	Data  map[string]any
	*Delimiters
	SubPath string
}

var _ Step = &goTemplateSubstitutionStep{}

type Delimiters struct {
	Left  string
	Right string
}

func NewGoTemplateBasedSubstitutionStep(
	subPath string,
	funcs template.FuncMap,
	data map[string]any,
	delimiters *Delimiters,
) Step {
	return &goTemplateSubstitutionStep{
		funcs:      funcs,
		Data:       data,
		Delimiters: delimiters,
		SubPath:    subPath,
	}
}

func (o *goTemplateSubstitutionStep) Substitute(path string) error {
	path = filepath.Join(path, o.SubPath)
	base := template.New(filepath.Base(path)).Funcs(o.funcs)
	if o.Delimiters != nil && o.Left != "" && o.Right != "" {
		base = base.Delims(o.Left, o.Right)
	}

	tpl, err := base.ParseFiles(path)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, o.Data); err != nil {
		return err
	}

	if err := os.WriteFile(path, buf.Bytes(), os.ModePerm); err != nil {
		return fmt.Errorf("failed to write template result back to file: %w", err)
	}

	return nil
}
