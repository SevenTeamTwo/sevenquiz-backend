package api

import "time"

type Question struct {
	ID         int           `json:"id"                   yaml:"ID"`
	Title      string        `json:"title"                yaml:"Title"`
	Type       string        `json:"type"                 yaml:"Type"`
	Time       time.Duration `json:"time"                 yaml:"Time"`
	Medias     []Media       `json:"medias,omitempty"     yaml:"Medias"`
	Choices    []string      `json:"choices,omitempty"    yaml:"Choices"`
	OrderItems []OrderItem   `json:"orderItems,omitempty" yaml:"OrderItems"`
	Categories []string      `json:"categories,omitempty" yaml:"Categories"`
	Options    any           `json:"options,omitempty"    yaml:"Options"`
	Answer     *Answer       `json:"answer,omitempty"     yaml:"Answer"`
}

type Answer struct {
	X       int      `json:"x,omitempty"       yaml:"X"`
	Y       int      `json:"y,omitempty"       yaml:"Y"`
	Text    string   `json:"text,omitempty"    yaml:"Text"`
	Choices []string `json:"choices,omitempty" yaml:"Choices"`
	Order   []string `json:"order,omitempty"   yaml:"Order"`
}

type Media struct {
	Path string `json:"path,omitempty" yaml:"Path"`
	Type string `json:"type,omitempty" yaml:"Type"`
}

type OrderItem struct {
	Name  string `json:"name,omitempty"  yaml:"Name"`
	Media Media  `json:"media,omitempty" yaml:"Media"`
}

type ChoicesOptions struct {
	MinChoices uint `json:"minChoices,omitempty" yaml:"MinChoices"`
	MaxChoices uint `json:"maxChoices,omitempty" yaml:"MaxChoices"`
}
