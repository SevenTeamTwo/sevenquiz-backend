package api

type Quiz struct {
	Name      string     `json:"name"`
	Questions []Question `json:"questions"`
}
