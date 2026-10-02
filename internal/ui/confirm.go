package ui

// Confirm asks a yes/no question on one line (y/n, ←/→, Enter takes the
// highlighted answer, def initially).
func Confirm(title string, def bool) (bool, error) {
	v, err := confirmSelect(title, def).Run()
	return v == "Yes", err
}

func confirmSelect(title string, def bool) Select {
	s := Select{
		Title:  title,
		Inline: true,
		Items:  []Item{{Label: "Yes", Keys: []string{"y", "Y"}}, {Label: "No", Keys: []string{"n", "N"}}},
	}
	if !def {
		s.Default = 1
	}
	return s
}
