package mandatory

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// InjectionCase is the output rule for one payload in testdata/injections.
// MustNotAppear lists fragments that must stay out of the digest, the post and the mail.
type InjectionCase struct {
	ID            string   `yaml:"id"`
	MustNotAppear []string `yaml:"must_not_appear"`
}

// InjectionSet is testdata/injections/expected.yaml.
// Recipients, the event count and the event order of the neutral set stay as they were.
// RulesUnchanged means the system rules are not replaced by the payload.
type InjectionSet struct {
	Recipients      []string        `yaml:"recipients"`
	EventsUnchanged bool            `yaml:"events_unchanged"`
	OrderUnchanged  bool            `yaml:"order_unchanged"`
	RulesUnchanged  bool            `yaml:"rules_unchanged"`
	Cases           []InjectionCase `yaml:"cases"`
}

// LoadInjections reads the injection expectations.
func LoadInjections(path string) (InjectionSet, error) {
	var set InjectionSet
	if err := readYAML(path, &set); err != nil {
		return InjectionSet{}, err
	}
	if set.Recipients == nil {
		set.Recipients = []string{}
	}
	if !set.EventsUnchanged || !set.OrderUnchanged || !set.RulesUnchanged {
		return InjectionSet{}, fmt.Errorf("ожидания инъекций: число, порядок или правила помечены как изменяемые")
	}
	seen := map[string]bool{}
	for _, item := range set.Cases {
		if item.ID == "" {
			return InjectionSet{}, fmt.Errorf("ожидания инъекций: пустой id")
		}
		if seen[item.ID] {
			return InjectionSet{}, fmt.Errorf("ожидания инъекций: повтор %s", item.ID)
		}
		seen[item.ID] = true
		if len(item.MustNotAppear) == 0 {
			return InjectionSet{}, fmt.Errorf("ожидания инъекций: у %s нет запрещённых фрагментов", item.ID)
		}
	}
	return set, nil
}

// HardCase describes one difficult input. The run must not stop, and the journal must stay complete.
type HardCase struct {
	File   string `yaml:"file"`
	Kind   string `yaml:"kind"`
	Expect string `yaml:"expect"`
}

// HardSet is testdata/hard/expectations.yaml.
type HardSet struct {
	RunMustNotFail  bool       `yaml:"run_must_not_fail"`
	JournalComplete bool       `yaml:"journal_complete"`
	Cases           []HardCase `yaml:"cases"`
}

// LoadHard reads expectations for difficult inputs.
func LoadHard(path string) (HardSet, error) {
	var set HardSet
	if err := readYAML(path, &set); err != nil {
		return HardSet{}, err
	}
	if !set.RunMustNotFail || !set.JournalComplete {
		return HardSet{}, fmt.Errorf("ожидания трудных входов: прогон или журнал не помечены как обязательные")
	}
	if len(set.Cases) == 0 {
		return HardSet{}, fmt.Errorf("ожидания трудных входов: список пуст")
	}
	for _, item := range set.Cases {
		if item.File == "" || item.Kind == "" || item.Expect == "" {
			return HardSet{}, fmt.Errorf("ожидания трудных входов: неполная запись %q", item.File)
		}
	}
	return set, nil
}

func readYAML(path string, dest any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("чтение %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("разбор %s: %w", path, err)
	}
	return nil
}
