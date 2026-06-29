package swayipc

type Node struct {
	ID               int64             `json:"id"`
	Name             string            `json:"name"`
	Type             string            `json:"type"`
	AppID            string            `json:"app_id"`
	PID              int               `json:"pid"`
	Focused          bool              `json:"focused"`
	Nodes            []Node            `json:"nodes"`
	FloatingNodes    []Node            `json:"floating_nodes"`
	WindowProperties *WindowProperties `json:"window_properties"`
}

type WindowProperties struct {
	Class    string `json:"class"`
	Instance string `json:"instance"`
	Title    string `json:"title"`
}

type FocusedContainer struct {
	ID        int64
	Name      string
	AppID     string
	Class     string
	PID       int
	Workspace string
	Output    string
}

func FindFocused(root Node) (FocusedContainer, bool) {
	return findFocused(root, "", "")
}

func findFocused(n Node, workspace, output string) (FocusedContainer, bool) {
	if n.Type == "workspace" && n.Name != "" {
		workspace = n.Name
	}
	if n.Type == "output" && n.Name != "" {
		output = n.Name
	}
	if n.Focused {
		class := ""
		if n.WindowProperties != nil {
			class = n.WindowProperties.Class
		}
		return FocusedContainer{
			ID:        n.ID,
			Name:      n.Name,
			AppID:     n.AppID,
			Class:     class,
			PID:       n.PID,
			Workspace: workspace,
			Output:    output,
		}, true
	}
	for _, child := range n.Nodes {
		if f, ok := findFocused(child, workspace, output); ok {
			return f, true
		}
	}
	for _, child := range n.FloatingNodes {
		if f, ok := findFocused(child, workspace, output); ok {
			return f, true
		}
	}
	return FocusedContainer{}, false
}
