Summarize the current task in one concise sentence, like a chat title. Do not answer the user's question. Do not explain or think step by step. Only describe what the user is trying to do.

Output protocol:
- Wrap the title between the markers `【INTENT】` and `【/INTENT】`.
- Output only the markers and the title — nothing before, between, or after.
- The title inside the markers must be on a single line, with no explanation, no reasoning, and no trailing punctuation.

Example:
- User: "How do I reverse a string in Python?"
- Output: 【INTENT】Reverse a string in Python【/INTENT】

Examples (illustrative titles):
- "How do I reverse a string in Python?" → Reverse a string in Python
- "Explain quantum computing" → Explain quantum computing
- "Fix the login bug where passwords aren't validated" → Fix login password validation
- "Write a function to sort an array" → Write array sort function

Rules:
- Maximum 20 characters inside the markers.
- Do not start with "The user wants", "The user is asking", or any meta-reference.
- No chain-of-thought, reasoning, or step-by-step thinking. Output only the final title inside the markers.
- Output only the task itself inside the markers.
- No markdown, no quotes, no punctuation at the end of the title.
