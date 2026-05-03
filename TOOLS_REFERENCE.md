# Tools, Skills, and Commands Reference

## Skills — User Scope

> Invoke pattern: `Skill(skill="<name>")`. Names without a colon prefix are user-scope.

| Name | How to Call | What It Does |
|---|---|---|
| `update-config` | `Skill(skill="update-config")` | Configure Claude Code harness via settings.json — hooks, permissions, env vars, automations |
| `keybindings-help` | `Skill(skill="keybindings-help")` | Customize keyboard shortcuts in `~/.claude/keybindings.json` |
| `fewer-permission-prompts` | `Skill(skill="fewer-permission-prompts")` | Scan transcripts and add an allowlist to project settings to reduce permission prompts |
| `simplify` | `Skill(skill="simplify")` | Review changed code for reuse/quality/efficiency, then fix issues found |
| `loop` | `Skill(skill="loop")` | Run a prompt or slash command on a recurring interval (e.g. `/loop 5m /foo`) |
| `schedule` | `Skill(skill="schedule")` | Create/manage scheduled remote agents on a cron schedule |
| `claude-api` | `Skill(skill="claude-api")` | Build, debug, optimize Claude API / Anthropic SDK apps (caching, tool use, model migration) |
| `code-review` | `Skill(skill="code-review")` | Review existing diff/branch/PR before commit/merge — does NOT write new code |
| `debug` | `Skill(skill="debug")` | Diagnose and fix bugs, failing tests, production errors |
| `docs-update` | `Skill(skill="docs-update")` | Update stale docs after code changes (API, architecture, thesis, loading-contract) |
| `execute` | `Skill(skill="execute")` | Implement a well-defined solution where the design/spec is already settled |
| `github-tracking` | `Skill(skill="github-tracking")` | Manage GitHub issues — create, log progress, transition labels through lifecycle |
| `local-ci` | `Skill(skill="local-ci")` | Run lint/type/tests locally on changed files before commit |
| `plan-and-create` | `Skill(skill="plan-and-create")` | Turn a high-level idea into a working feature — design → implement → validate |
| `refactor` | `Skill(skill="refactor")` | Improve structure/readability of existing code without changing behavior |
| `research` | `Skill(skill="research")` | Research unfamiliar libraries, APIs, or design approaches |
| `review-prep` | `Skill(skill="review-prep")` | Prepare changes for PR/handoff before reviewer pickup |
| `session-startup` | `Skill(skill="session-startup")` | Catch up on an unfamiliar project or resume after time away |
| `tdd-setup` | `Skill(skill="tdd-setup")` | Write failing tests before implementation — the RED phase of TDD |

---

## Skills — `superpowers:*` Plugin

| Name | How to Call | What It Does |
|---|---|---|
| `superpowers:using-superpowers` | `Skill(skill="superpowers:using-superpowers")` | Bootstrap rules for finding/using skills — auto-loaded at session start |
| `superpowers:brainstorming` | `Skill(skill="superpowers:brainstorming")` | MUST use before any creative work — explores user intent, requirements, design |
| `superpowers:writing-plans` | `Skill(skill="superpowers:writing-plans")` | Author implementation plans for multi-step tasks before touching code |
| `superpowers:executing-plans` | `Skill(skill="superpowers:executing-plans")` | Execute a written plan in a separate session with review checkpoints |
| `superpowers:subagent-driven-development` | `Skill(skill="superpowers:subagent-driven-development")` | Execute implementation plans using subagents in the current session |
| `superpowers:dispatching-parallel-agents` | `Skill(skill="superpowers:dispatching-parallel-agents")` | Coordinate 2+ independent tasks across parallel subagents |
| `superpowers:test-driven-development` | `Skill(skill="superpowers:test-driven-development")` | Enforce test-first discipline for any feature or bugfix |
| `superpowers:systematic-debugging` | `Skill(skill="superpowers:systematic-debugging")` | Structured root-cause investigation before proposing fixes |
| `superpowers:verification-before-completion` | `Skill(skill="superpowers:verification-before-completion")` | Run verification commands and confirm output before claiming done |
| `superpowers:requesting-code-review` | `Skill(skill="superpowers:requesting-code-review")` | Request review when tasks complete or before merging |
| `superpowers:receiving-code-review` | `Skill(skill="superpowers:receiving-code-review")` | Process incoming review feedback rigorously — verify, don't blindly comply |
| `superpowers:finishing-a-development-branch` | `Skill(skill="superpowers:finishing-a-development-branch")` | Decide how to integrate completed work — merge / PR / cleanup options |
| `superpowers:using-git-worktrees` | `Skill(skill="superpowers:using-git-worktrees")` | Isolate feature work in worktrees before executing plans |
| `superpowers:writing-skills` | `Skill(skill="superpowers:writing-skills")` | Create, edit, or verify skills before deployment |
| `superpowers:brainstorm` *(deprecated)* | Use `superpowers:brainstorming` instead | Deprecated alias |
| `superpowers:write-plan` *(deprecated)* | Use `superpowers:writing-plans` instead | Deprecated alias |
| `superpowers:execute-plan` *(deprecated)* | Use `superpowers:executing-plans` instead | Deprecated alias |

---

## Skills — Other Plugins

| Name | How to Call | What It Does |
|---|---|---|
| `code-review:code-review` | `Skill(skill="code-review:code-review")` | Code review a pull request (plugin variant) |
| `feature-dev:feature-dev` | `Skill(skill="feature-dev:feature-dev")` | Guided feature development with codebase understanding + architecture focus |
| `frontend-design:frontend-design` | `Skill(skill="frontend-design:frontend-design")` | Build distinctive, production-grade frontend UI avoiding generic AI aesthetics |
| `ralph-loop:ralph-loop` | `Skill(skill="ralph-loop:ralph-loop")` | Start Ralph Loop in current session (autonomous iteration) |
| `ralph-loop:cancel-ralph` | `Skill(skill="ralph-loop:cancel-ralph")` | Cancel an active Ralph Loop |
| `ralph-loop:help` | `Skill(skill="ralph-loop:help")` | Explain Ralph Loop plugin and commands |
| `figma:figma-use` | `Skill(skill="figma:figma-use")` | **MANDATORY** before any `use_figma` call — foundation for Plugin API writes/JS reads |
| `figma:figma-use-figjam` | `Skill(skill="figma:figma-use-figjam")` | Use `use_figma` in FigJam (whiteboard) context |
| `figma:figma-code-connect` | `Skill(skill="figma:figma-code-connect")` | Create/maintain Code Connect template files mapping Figma components → code |
| `figma:figma-generate-design` | `Skill(skill="figma:figma-generate-design")` | Translate code/page → Figma (write app pages, modals, panels into Figma) |
| `figma:figma-generate-diagram` | `Skill(skill="figma:figma-generate-diagram")` | **MANDATORY** before `generate_diagram` — routes flowchart vs architecture |
| `figma:figma-generate-library` | `Skill(skill="figma:figma-generate-library")` | Build/update a design system in Figma from a codebase |
| `figma:figma-create-design-system-rules` | `Skill(skill="figma:figma-create-design-system-rules")` | Generate custom design system rules for the codebase |
| `figma:figma-implement-design` | `Skill(skill="figma:figma-implement-design")` | Translate Figma design → production code with 1:1 visual fidelity |
| `claude-mem:mem-search` | `Skill(skill="claude-mem:mem-search")` | Search claude-mem persistent cross-session memory database |
| `claude-mem:smart-explore` | `Skill(skill="claude-mem:smart-explore")` | Token-optimized AST-based code search via tree-sitter |
| `claude-mem:knowledge-agent` | `Skill(skill="claude-mem:knowledge-agent")` | Build/query AI knowledge bases ("brains") from observation history |
| `claude-mem:timeline-report` | `Skill(skill="claude-mem:timeline-report")` | Generate "Journey Into [Project]" narrative report from full timeline |
| `claude-mem:make-plan` | `Skill(skill="claude-mem:make-plan")` | Create a phased implementation plan with documentation discovery |
| `claude-mem:do` | `Skill(skill="claude-mem:do")` | Execute a phased plan using subagents (pairs with `make-plan`) |
| `claude-mem:version-bump` | `Skill(skill="claude-mem:version-bump")` | Automated semantic versioning + release workflow for plugins |

---

## Slash Commands

> Slash commands are **typed by the user** in the terminal. The agent does not call them programmatically.

| Name | How to Call | What It Does |
|---|---|---|
| `/init` | User types `/init` | Initialize a new `CLAUDE.md` with codebase documentation |
| `/review` | User types `/review` | Review the current pull request |
| `/security-review` | User types `/security-review` | Security audit of pending changes on the current branch |
| `/clear` | User types `/clear` | Clear the conversation context |
| `/resume` | User types `/resume` | Resume a prior session |
| `/help` | User types `/help` | Get help with using Claude Code |
| `/config` | User types `/config` | Adjust simple settings (theme, model) |
| `/fast` | User types `/fast` | Toggle Opus 4.6 fast mode |
| `/loop` | User types `/loop <interval> <prompt>` | Run a prompt/command repeatedly on an interval |
| `/schedule` | User types `/schedule` | Manage scheduled remote agents (cron routines) |
| `/ultrareview` | User types `/ultrareview` or `/ultrareview <PR#>` | Multi-agent cloud code review of current branch / PR |

---

## MCP Tools — Reasoning / AI

### `zen` (default model: `gpt-5.2-pro`)

| Name | How to Call | What It Does |
|---|---|---|
| `mcp__zen__chat` | `mcp__zen__chat(...)` | General-purpose chat with a chosen external model |
| `mcp__zen__clink` | `mcp__zen__clink(...)` | Link/relay between multiple model conversations |
| `mcp__zen__challenge` | `mcp__zen__challenge(...)` | Challenge a previous answer / stress-test a claim |
| `mcp__zen__consensus` | `mcp__zen__consensus(...)` | Multi-model consensus on a question |
| `mcp__zen__thinkdeep` | `mcp__zen__thinkdeep(...)` | Deep thinking on architecture and complex problems |
| `mcp__zen__planner` | `mcp__zen__planner(...)` | Multi-step plan generation by an external model |
| `mcp__zen__analyze` | `mcp__zen__analyze(...)` | Code/file analysis pass |
| `mcp__zen__codereview` | `mcp__zen__codereview(...)` | Systematic code review by an external model |
| `mcp__zen__refactor` | `mcp__zen__refactor(...)` | Refactor suggestions from an external model |
| `mcp__zen__tracer` | `mcp__zen__tracer(...)` | Trace execution paths through a codebase |
| `mcp__zen__precommit` | `mcp__zen__precommit(...)` | Pre-commit validation pass |
| `mcp__zen__secaudit` | `mcp__zen__secaudit(...)` | Security audit pass on changed code |
| `mcp__zen__testgen` | `mcp__zen__testgen(...)` | Generate tests for given code |
| `mcp__zen__debug` | `mcp__zen__debug(...)` | Root-cause analysis for bugs |
| `mcp__zen__docgen` | `mcp__zen__docgen(...)` | Generate documentation for code |
| `mcp__zen__apilookup` | `mcp__zen__apilookup(...)` | Look up API references via external models |
| `mcp__zen__listmodels` | `mcp__zen__listmodels(...)` | List available zen models |
| `mcp__zen__version` | `mcp__zen__version(...)` | Get zen MCP version info |

### `sequential-thinking`

| Name | How to Call | What It Does |
|---|---|---|
| `mcp__sequential-thinking__sequentialthinking` | `mcp__sequential-thinking__sequentialthinking(...)` | Break complex tasks into smaller sequential reasoning steps |

---

## MCP Tools — Research / Documentation

### `context7` (preferred over web search for library docs)

| Name | How to Call | What It Does |
|---|---|---|
| `mcp__context7__resolve-library-id` | `mcp__context7__resolve-library-id(libraryName)` | Find canonical Context7 ID for a library/framework name |
| `mcp__context7__query-docs` | `mcp__context7__query-docs(libraryId, topic, ...)` | Query current docs for a resolved library ID |

### `perplexity-ask`

| Name | How to Call | What It Does |
|---|---|---|
| `mcp__perplexity-ask__perplexity_ask` | `mcp__perplexity-ask__perplexity_ask(messages)` | Web research / "how do people solve X" via Perplexity |

---

## MCP Tools — Memory

### `memory`

| Name | How to Call | What It Does |
|---|---|---|
| `mcp__memory__create_entities` | `mcp__memory__create_entities(...)` | Create entities in the persistent knowledge graph |
| `mcp__memory__create_relations` | `mcp__memory__create_relations(...)` | Create relationships between entities |
| `mcp__memory__add_observations` | `mcp__memory__add_observations(...)` | Add observations to existing entities |
| `mcp__memory__delete_entities` | `mcp__memory__delete_entities(...)` | Delete entities from the graph |
| `mcp__memory__delete_relations` | `mcp__memory__delete_relations(...)` | Delete relationships between entities |
| `mcp__memory__delete_observations` | `mcp__memory__delete_observations(...)` | Delete observations from an entity |
| `mcp__memory__read_graph` | `mcp__memory__read_graph(...)` | Read the full knowledge graph |
| `mcp__memory__search_nodes` | `mcp__memory__search_nodes(query)` | Search nodes by query string |
| `mcp__memory__open_nodes` | `mcp__memory__open_nodes(names)` | Open specific named nodes for inspection |

### `plugin_claude-mem_mcp-search`

| Name | How to Call | What It Does |
|---|---|---|
| `mcp__plugin_claude-mem_mcp-search__search` | `mcp__plugin_claude-mem_mcp-search__search(...)` | Basic search across claude-mem corpus |
| `mcp__plugin_claude-mem_mcp-search__smart_search` | `mcp__plugin_claude-mem_mcp-search__smart_search(...)` | Semantic + structured search across claude-mem |
| `mcp__plugin_claude-mem_mcp-search__smart_outline` | `mcp__plugin_claude-mem_mcp-search__smart_outline(...)` | Generate an outline of search results |
| `mcp__plugin_claude-mem_mcp-search__smart_unfold` | `mcp__plugin_claude-mem_mcp-search__smart_unfold(...)` | Expand a node found via search to full detail |
| `mcp__plugin_claude-mem_mcp-search__timeline` | `mcp__plugin_claude-mem_mcp-search__timeline(...)` | Project timeline view across observations |
| `mcp__plugin_claude-mem_mcp-search__query_corpus` | `mcp__plugin_claude-mem_mcp-search__query_corpus(...)` | Query a specific named corpus |
| `mcp__plugin_claude-mem_mcp-search__list_corpora` | `mcp__plugin_claude-mem_mcp-search__list_corpora(...)` | List available corpora |
| `mcp__plugin_claude-mem_mcp-search__build_corpus` | `mcp__plugin_claude-mem_mcp-search__build_corpus(...)` | Build a new corpus from observations |
| `mcp__plugin_claude-mem_mcp-search__rebuild_corpus` | `mcp__plugin_claude-mem_mcp-search__rebuild_corpus(...)` | Rebuild an existing corpus |
| `mcp__plugin_claude-mem_mcp-search__prime_corpus` | `mcp__plugin_claude-mem_mcp-search__prime_corpus(...)` | Prime / warm up a corpus for fast queries |
| `mcp__plugin_claude-mem_mcp-search__reprime_corpus` | `mcp__plugin_claude-mem_mcp-search__reprime_corpus(...)` | Re-prime an existing corpus |
| `mcp__plugin_claude-mem_mcp-search__get_observations` | `mcp__plugin_claude-mem_mcp-search__get_observations(...)` | Retrieve observations by criteria |
| `mcp__plugin_claude-mem_mcp-search___IMPORTANT` | `mcp__plugin_claude-mem_mcp-search___IMPORTANT(...)` | Internal corpus instructions / usage hints |

---


## MCP Tools — Browser Automation

### `claude-in-chrome` *(MUST `ToolSearch` each tool before calling)*

| Name | How to Call | What It Does |
|---|---|---|
| `mcp__claude-in-chrome__tabs_context_mcp` | `ToolSearch("select:mcp__claude-in-chrome__tabs_context_mcp", 1)` → call. **Always call this first each session.** | Get current tab context — must run before any other Chrome action |
| `mcp__claude-in-chrome__tabs_create_mcp` | `ToolSearch(...)` → `mcp__claude-in-chrome__tabs_create_mcp(...)` | Open a new tab |
| `mcp__claude-in-chrome__tabs_close_mcp` | `ToolSearch(...)` → call | Close a tab by ID |
| `mcp__claude-in-chrome__navigate` | `ToolSearch(...)` → call | Navigate the current tab to a URL |
| `mcp__claude-in-chrome__switch_browser` | `ToolSearch(...)` → call | Switch active browser instance |
| `mcp__claude-in-chrome__resize_window` | `ToolSearch(...)` → call | Resize the browser window |
| `mcp__claude-in-chrome__read_page` | `ToolSearch(...)` → call | Read full page content (DOM + structure) |
| `mcp__claude-in-chrome__get_page_text` | `ToolSearch(...)` → call | Get plain-text page contents |
| `mcp__claude-in-chrome__read_console_messages` | `ToolSearch(...)` → call (use `pattern` regex param to filter) | Read browser console output with regex filter |
| `mcp__claude-in-chrome__read_network_requests` | `ToolSearch(...)` → call | Read network request log for the page |
| `mcp__claude-in-chrome__find` | `ToolSearch(...)` → call | Find elements on page by selector/text |
| `mcp__claude-in-chrome__computer` | `ToolSearch(...)` → call | Computer-vision-style page interaction |
| `mcp__claude-in-chrome__form_input` | `ToolSearch(...)` → call | Fill form inputs |
| `mcp__claude-in-chrome__file_upload` | `ToolSearch(...)` → call | Upload a file via the page |
| `mcp__claude-in-chrome__upload_image` | `ToolSearch(...)` → call | Upload an image via the page |
| `mcp__claude-in-chrome__javascript_tool` | `ToolSearch(...)` → call | Execute arbitrary JavaScript in the page |
| `mcp__claude-in-chrome__browser_batch` | `ToolSearch(...)` → call | Batch multiple browser actions in one call |
| `mcp__claude-in-chrome__gif_creator` | `ToolSearch(...)` → call (use for multi-step recordings) | Record a GIF of an interaction sequence |
| `mcp__claude-in-chrome__shortcuts_list` | `ToolSearch(...)` → call | List configured browser shortcuts |
| `mcp__claude-in-chrome__shortcuts_execute` | `ToolSearch(...)` → call | Execute a configured browser shortcut |

### `puppeteer`

| Name | How to Call | What It Does |
|---|---|---|
| `mcp__puppeteer__puppeteer_navigate` | `mcp__puppeteer__puppeteer_navigate(url)` | Navigate to a URL |
| `mcp__puppeteer__puppeteer_click` | `mcp__puppeteer__puppeteer_click(selector)` | Click element by selector |
| `mcp__puppeteer__puppeteer_fill` | `mcp__puppeteer__puppeteer_fill(selector, value)` | Fill input field |
| `mcp__puppeteer__puppeteer_select` | `mcp__puppeteer__puppeteer_select(selector, value)` | Select dropdown option |
| `mcp__puppeteer__puppeteer_hover` | `mcp__puppeteer__puppeteer_hover(selector)` | Hover over an element |
| `mcp__puppeteer__puppeteer_screenshot` | `mcp__puppeteer__puppeteer_screenshot(name, ...)` | Capture screenshot |
| `mcp__puppeteer__puppeteer_evaluate` | `mcp__puppeteer__puppeteer_evaluate(script)` | Run JavaScript in page context |

### `plugin_playwright_playwright`

| Name | How to Call | What It Does |
|---|---|---|
| `mcp__plugin_playwright_playwright__browser_navigate` | `mcp__plugin_playwright_playwright__browser_navigate(url)` | Navigate to a URL |
| `mcp__plugin_playwright_playwright__browser_navigate_back` | `mcp__plugin_playwright_playwright__browser_navigate_back(...)` | Browser back |
| `mcp__plugin_playwright_playwright__browser_close` | `mcp__plugin_playwright_playwright__browser_close(...)` | Close the browser |
| `mcp__plugin_playwright_playwright__browser_resize` | `mcp__plugin_playwright_playwright__browser_resize(...)` | Resize the browser window |
| `mcp__plugin_playwright_playwright__browser_tabs` | `mcp__plugin_playwright_playwright__browser_tabs(...)` | Manage tabs (list / switch / create / close) |
| `mcp__plugin_playwright_playwright__browser_click` | `mcp__plugin_playwright_playwright__browser_click(...)` | Click an element |
| `mcp__plugin_playwright_playwright__browser_hover` | `mcp__plugin_playwright_playwright__browser_hover(...)` | Hover over an element |
| `mcp__plugin_playwright_playwright__browser_drag` | `mcp__plugin_playwright_playwright__browser_drag(...)` | Drag an element |
| `mcp__plugin_playwright_playwright__browser_drop` | `mcp__plugin_playwright_playwright__browser_drop(...)` | Drop a dragged element |
| `mcp__plugin_playwright_playwright__browser_type` | `mcp__plugin_playwright_playwright__browser_type(...)` | Type text into element |
| `mcp__plugin_playwright_playwright__browser_press_key` | `mcp__plugin_playwright_playwright__browser_press_key(...)` | Send keyboard key |
| `mcp__plugin_playwright_playwright__browser_select_option` | `mcp__plugin_playwright_playwright__browser_select_option(...)` | Select dropdown option |
| `mcp__plugin_playwright_playwright__browser_fill_form` | `mcp__plugin_playwright_playwright__browser_fill_form(...)` | Fill multiple form fields at once |
| `mcp__plugin_playwright_playwright__browser_file_upload` | `mcp__plugin_playwright_playwright__browser_file_upload(...)` | Upload a file |
| `mcp__plugin_playwright_playwright__browser_snapshot` | `mcp__plugin_playwright_playwright__browser_snapshot(...)` | Capture accessibility / DOM snapshot |
| `mcp__plugin_playwright_playwright__browser_take_screenshot` | `mcp__plugin_playwright_playwright__browser_take_screenshot(...)` | Capture screenshot |
| `mcp__plugin_playwright_playwright__browser_console_messages` | `mcp__plugin_playwright_playwright__browser_console_messages(...)` | Read browser console messages |
| `mcp__plugin_playwright_playwright__browser_network_requests` | `mcp__plugin_playwright_playwright__browser_network_requests(...)` | Read network requests |
| `mcp__plugin_playwright_playwright__browser_evaluate` | `mcp__plugin_playwright_playwright__browser_evaluate(script)` | Run JavaScript in page context |
| `mcp__plugin_playwright_playwright__browser_run_code` | `mcp__plugin_playwright_playwright__browser_run_code(...)` | Run code in page context |
| `mcp__plugin_playwright_playwright__browser_handle_dialog` | `mcp__plugin_playwright_playwright__browser_handle_dialog(...)` | Accept/dismiss browser dialogs (alert/confirm/prompt) |
| `mcp__plugin_playwright_playwright__browser_wait_for` | `mcp__plugin_playwright_playwright__browser_wait_for(...)` | Wait for selector or condition |

---

## MCP Tools — Productivity / SaaS

### `claude_ai_Gmail`

| Name | How to Call | What It Does |
|---|---|---|
| `mcp__claude_ai_Gmail__authenticate` | `mcp__claude_ai_Gmail__authenticate(...)` | Begin Gmail OAuth flow |
| `mcp__claude_ai_Gmail__complete_authentication` | `mcp__claude_ai_Gmail__complete_authentication(...)` | Finalize Gmail OAuth flow |

### `claude_ai_Google_Calendar`

| Name | How to Call | What It Does |
|---|---|---|
| `mcp__claude_ai_Google_Calendar__authenticate` | `mcp__claude_ai_Google_Calendar__authenticate(...)` | Begin Google Calendar OAuth flow |
| `mcp__claude_ai_Google_Calendar__complete_authentication` | `mcp__claude_ai_Google_Calendar__complete_authentication(...)` | Finalize Google Calendar OAuth flow |

### `claude_ai_Google_Drive`

| Name | How to Call | What It Does |
|---|---|---|
| `mcp__claude_ai_Google_Drive__authenticate` | `mcp__claude_ai_Google_Drive__authenticate(...)` | Begin Google Drive OAuth flow |
| `mcp__claude_ai_Google_Drive__complete_authentication` | `mcp__claude_ai_Google_Drive__complete_authentication(...)` | Finalize Google Drive OAuth flow |

### `claude_ai_Postman` *(read instructions resource first)*

| Name | How to Call | What It Does |
|---|---|---|
| `mcp__claude_ai_Postman__getAuthenticatedUser` | `mcp__claude_ai_Postman__getAuthenticatedUser(...)` | Get current authenticated Postman user |
| `mcp__claude_ai_Postman__getEnabledTools` | `mcp__claude_ai_Postman__getEnabledTools(...)` | List enabled Postman tools/resources |
| `mcp__claude_ai_Postman__getWorkspaces` | `mcp__claude_ai_Postman__getWorkspaces(...)` | List workspaces |
| `mcp__claude_ai_Postman__getWorkspace` | `mcp__claude_ai_Postman__getWorkspace(workspaceId)` | Read a workspace by ID |
| `mcp__claude_ai_Postman__createWorkspace` | `mcp__claude_ai_Postman__createWorkspace(...)` | Create a new workspace |
| `mcp__claude_ai_Postman__updateWorkspace` | `mcp__claude_ai_Postman__updateWorkspace(...)` | Update a workspace |
| `mcp__claude_ai_Postman__getCollections` | `mcp__claude_ai_Postman__getCollections(...)` | List collections |
| `mcp__claude_ai_Postman__getCollection` | `mcp__claude_ai_Postman__getCollection(collectionId)` | Read a collection by ID |
| `mcp__claude_ai_Postman__createCollection` | `mcp__claude_ai_Postman__createCollection(...)` | Create a collection |
| `mcp__claude_ai_Postman__putCollection` | `mcp__claude_ai_Postman__putCollection(...)` | Replace an existing collection |
| `mcp__claude_ai_Postman__duplicateCollection` | `mcp__claude_ai_Postman__duplicateCollection(...)` | Duplicate a collection (async task) |
| `mcp__claude_ai_Postman__getDuplicateCollectionTaskStatus` | `mcp__claude_ai_Postman__getDuplicateCollectionTaskStatus(...)` | Check duplicate-collection task progress |
| `mcp__claude_ai_Postman__generateCollection` | `mcp__claude_ai_Postman__generateCollection(...)` | Generate a collection from a spec |
| `mcp__claude_ai_Postman__getGeneratedCollectionSpecs` | `mcp__claude_ai_Postman__getGeneratedCollectionSpecs(...)` | Track generation tasks for collections |
| `mcp__claude_ai_Postman__createCollectionRequest` | `mcp__claude_ai_Postman__createCollectionRequest(...)` | Add a request to a collection |
| `mcp__claude_ai_Postman__updateCollectionRequest` | `mcp__claude_ai_Postman__updateCollectionRequest(...)` | Update a request inside a collection |
| `mcp__claude_ai_Postman__createCollectionResponse` | `mcp__claude_ai_Postman__createCollectionResponse(...)` | Add an example response to a request |
| `mcp__claude_ai_Postman__getAllSpecs` | `mcp__claude_ai_Postman__getAllSpecs(...)` | List all API specs |
| `mcp__claude_ai_Postman__getSpec` | `mcp__claude_ai_Postman__getSpec(specId)` | Read an API spec by ID |
| `mcp__claude_ai_Postman__getSpecDefinition` | `mcp__claude_ai_Postman__getSpecDefinition(...)` | Read a spec's definition document |
| `mcp__claude_ai_Postman__createSpec` | `mcp__claude_ai_Postman__createSpec(...)` | Create a new API spec |
| `mcp__claude_ai_Postman__updateSpecProperties` | `mcp__claude_ai_Postman__updateSpecProperties(...)` | Update a spec's metadata |
| `mcp__claude_ai_Postman__getSpecFiles` | `mcp__claude_ai_Postman__getSpecFiles(...)` | List files in a spec |
| `mcp__claude_ai_Postman__getSpecFile` | `mcp__claude_ai_Postman__getSpecFile(...)` | Read a single spec file |
| `mcp__claude_ai_Postman__createSpecFile` | `mcp__claude_ai_Postman__createSpecFile(...)` | Create a file inside a spec |
| `mcp__claude_ai_Postman__updateSpecFile` | `mcp__claude_ai_Postman__updateSpecFile(...)` | Update a file inside a spec |
| `mcp__claude_ai_Postman__getSpecCollections` | `mcp__claude_ai_Postman__getSpecCollections(...)` | List collections linked to a spec |
| `mcp__claude_ai_Postman__generateSpecFromCollection` | `mcp__claude_ai_Postman__generateSpecFromCollection(...)` | Generate a spec from a collection |
| `mcp__claude_ai_Postman__syncCollectionWithSpec` | `mcp__claude_ai_Postman__syncCollectionWithSpec(...)` | Sync a collection with its source spec |
| `mcp__claude_ai_Postman__syncSpecWithCollection` | `mcp__claude_ai_Postman__syncSpecWithCollection(...)` | Sync a spec with its source collection |
| `mcp__claude_ai_Postman__getEnvironments` | `mcp__claude_ai_Postman__getEnvironments(...)` | List Postman environments |
| `mcp__claude_ai_Postman__getEnvironment` | `mcp__claude_ai_Postman__getEnvironment(...)` | Read an environment by ID |
| `mcp__claude_ai_Postman__createEnvironment` | `mcp__claude_ai_Postman__createEnvironment(...)` | Create an environment |
| `mcp__claude_ai_Postman__putEnvironment` | `mcp__claude_ai_Postman__putEnvironment(...)` | Replace an environment |
| `mcp__claude_ai_Postman__getMocks` | `mcp__claude_ai_Postman__getMocks(...)` | List mock servers |
| `mcp__claude_ai_Postman__getMock` | `mcp__claude_ai_Postman__getMock(...)` | Read a mock server |
| `mcp__claude_ai_Postman__createMock` | `mcp__claude_ai_Postman__createMock(...)` | Create a mock server |
| `mcp__claude_ai_Postman__updateMock` | `mcp__claude_ai_Postman__updateMock(...)` | Update a mock server |
| `mcp__claude_ai_Postman__publishMock` | `mcp__claude_ai_Postman__publishMock(...)` | Publish a mock server |
| `mcp__claude_ai_Postman__getTaggedEntities` | `mcp__claude_ai_Postman__getTaggedEntities(...)` | List entities tagged with a label |

---

## MCP Tools — Design

### `plugin_figma_figma`

| Name | How to Call | What It Does |
|---|---|---|
| `mcp__plugin_figma_figma__authenticate` | `mcp__plugin_figma_figma__authenticate(...)` | Begin Figma OAuth flow |
| `mcp__plugin_figma_figma__complete_authentication` | `mcp__plugin_figma_figma__complete_authentication(...)` | Finalize Figma OAuth flow |

### `stitch`

| Name | How to Call | What It Does |
|---|---|---|
| `mcp__stitch__create_project` | `mcp__stitch__create_project(...)` | Create a new Stitch project |
| `mcp__stitch__get_project` | `mcp__stitch__get_project(...)` | Read a Stitch project |
| `mcp__stitch__list_projects` | `mcp__stitch__list_projects(...)` | List Stitch projects |
| `mcp__stitch__generate_screen_from_text` | `mcp__stitch__generate_screen_from_text(...)` | Generate UI screen from a text prompt |
| `mcp__stitch__get_screen` | `mcp__stitch__get_screen(...)` | Read a generated screen |
| `mcp__stitch__list_screens` | `mcp__stitch__list_screens(...)` | List screens in a project |
| `mcp__stitch__edit_screens` | `mcp__stitch__edit_screens(...)` | Edit existing screens |
| `mcp__stitch__generate_variants` | `mcp__stitch__generate_variants(...)` | Generate variant screens from a base |
| `mcp__stitch__create_design_system` | `mcp__stitch__create_design_system(...)` | Create a new design system |
| `mcp__stitch__update_design_system` | `mcp__stitch__update_design_system(...)` | Update an existing design system |
| `mcp__stitch__apply_design_system` | `mcp__stitch__apply_design_system(...)` | Apply a design system to screens |
| `mcp__stitch__list_design_systems` | `mcp__stitch__list_design_systems(...)` | List available design systems |

---

## How to Reference These in a Command or Subagent File

### Activating a Skill from an instruction file

```markdown
Before writing any implementation code, invoke the
`superpowers:test-driven-development` skill via the Skill tool.
Follow its checklist exactly.
```

The model translates that to: `Skill(skill="superpowers:test-driven-development")`.

### Calling an MCP tool from an instruction file

```markdown
When the user asks about a third-party library's API:
1. Call `mcp__context7__resolve-library-id` with the library name.
2. Then call `mcp__context7__query-docs` with the returned ID and a focused topic.
Do NOT answer from training data without consulting context7.
```

### Calling a deferred MCP tool (e.g., browser automation)

```markdown
Before any `mcp__claude-in-chrome__*` call, you MUST first run:
  ToolSearch(query="select:mcp__claude-in-chrome__<tool_name>", max_results=1)
Then call the tool.
```

### Subagent `tools:` allowlist

Each tool you reference must appear in the subagent's frontmatter `tools:` field, e.g.:

```markdown
---
name: api-researcher
tools: Read, Grep, Skill, mcp__context7__resolve-library-id, mcp__context7__query-docs
---
```

Without this, the subagent literally cannot make the call.

---

## Server-Specific Reminders

| Server | Reminder |
|---|---|
| `claude-in-chrome` | MUST `ToolSearch` to load schema before each tool call. Always start a session with `tabs_context_mcp`. Avoid actions that trigger JS dialogs (alert/confirm/prompt) — they freeze the extension. |
| `claude.ai Postman` | Read its instructions resource completely before answering API-related questions. |
| `context7` | Prefer over web search for library/framework docs. Do NOT use for refactoring, business-logic debugging, or generic programming concepts. |
| `zen` | Default model `gpt-5.2-pro` unless user names one (e.g., "use chat with gpt5"). |
