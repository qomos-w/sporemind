package agentkit

func mechanismPrompt(name string) string {
	data, err := mechanismPromptFS.ReadFile("prompts/" + name + ".md")
	if err != nil {
		panic("agentkit: mechanism prompt " + name + ": " + err.Error())
	}
	return string(data)
}
