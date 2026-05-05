package appstore

// UserForkInput captures the metadata fields the operator supplies in
// the "Template으로 저장" dialog. Compose YAML and env values are
// extracted by the server, not provided here.
type UserForkInput struct {
	Name        string
	Description string
	Category    string // empty → defaults to "내 Templates"
}
