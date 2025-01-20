package api

import "time"

type Question struct {
	Title      string        `yaml:"Title"`
	Type       string        `yaml:"Type"`
	Time       time.Duration `yaml:"Time"`
	Medias     []Media       `yaml:"Medias"`
	Choices    []string      `yaml:"Choices"`
	OrderItems []OrderItem   `yaml:"OrderItems"`
	Categories []string      `yaml:"Categories"`
	Options    any           `yaml:"Options"`
	Answer     Answer        `yaml:"Answer"`
}

type Answer struct {
	X, Y    int      `yaml:"X"`
	Text    string   `yaml:"Text"`
	Choices []string `yaml:"Choices"`
	Order   []string `yaml:"Order"`
}

type Media struct {
	Path string `yaml:"Path"`
	Type string `yaml:"Type"`
}

type OrderItem struct {
	Name  string `yaml:"Name,omitempty"`
	Media Media  `yaml:"Media,omitempty"`
}

type ChoicesOptions struct {
	MinChoices uint `yaml:"MinChoices"`
	MaxChoices uint `yaml:"MaxChoices"`
}
