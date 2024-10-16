package steps

type Step interface {
	Substitute(path string) error
}
