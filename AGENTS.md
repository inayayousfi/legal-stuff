# Repository interaction rules

- Treat every line printed by the CLI as user-facing product text.
- Numbered setup steps must describe actions that the user must perform. Do not expose internal bookkeeping as a user action.
- Use setup guidance to tell the user where to obtain required data and where to enter required data.
- Values written to `.env` are a private reference record. Storing a credential in `.env` does not configure the corresponding application.
- Do not tell the user to copy `QBIT_USER` or `QBIT_PASS` into qBittorrent unless the user explicitly asks for those manual steps.
- When the user must transfer a value collected by the CLI into an application, describe the action in ordinary language. Do not expose `.env` variable names as product instructions.
- Print required guidance before any input prompt that can prevent the user from reaching that guidance.
- When a guide refers to a value collected by a following prompt, say that the following prompt will request it. Do not hide the guide until after the value is entered.
- Apply the same interaction pattern to every service. Do not wait for the user to report the same presentation defect service by service.
- If a guide already gives the exact location of a value, the later input prompt must not print that location again.
- When a step needs a value already collected by the CLI, print the actual saved value directly in that numbered step. Do not make the user remember it or open `.env` to retrieve it.
- A user decision to print saved credentials is a usability policy. Apply it consistently to passwords, usernames, and API keys needed by later steps.
- State whether the user must create a value or whether the application generated it automatically. Do not make the user infer this from a field name.
- Put exact copyable values on lines without trailing sentence punctuation. Punctuation adjacent to an address, password, username, path, or API key can be mistaken for part of the value.
- Do not add explanatory paragraphs, headings, or workflow steps that the user did not request.
- Treat repeated user-facing information as a defect. Print each address, credential, instruction, and fact once at the point where the user needs it.
- Minimize cognitive cost. Merge related information into one block instead of making the user reconcile repeated or overlapping blocks.
- Preserve the exact requested scope. If a requested presentation change creates a technical limitation, state the limitation instead of silently replacing the requested behavior with another design.
