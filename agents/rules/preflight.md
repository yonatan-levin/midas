---
alwaysApply: true
---
# Pre-Flight Checklist Skill

When invoked with `@preflight`, execute this mandatory checklist before any implementation.

## Purpose

Ensures all required context is loaded, task is properly broken down, and MCP tools are utilized before coding begins.

## Automatic Actions

### Step 1: Break Down the Task

Use `sequential-thinking` MCP tool to:
1. Identify the task scope
2. Break into small, testable steps
3. Identify dependencies and blockers
4. Estimate complexity

### Step 2: Load Relevant Documentation

Based on the task, read these files using the Read tool:
- Root `CLAUDE.md` - project identity, tech stack, conventions, important files, build commands
- Root `AGENTS.md` - loading contract and cross-file relationships
- Root `docs/THESIS.md` - product direction, current phase, roadmap, scope boundaries
- Root `docs/API_DOCUMENTATION.md` - API reference, valuation engine internals, config, deployment
- Root `ARCHITECTURE.md` - overall system architecture
- Root `CONTRACTS.md` - API contracts and DTOs
- Root `TESTING.md` - testing requirements
- Service-specific docs if working in a service folder

### Step 3: Identify Role and Mode

Determine from context:
- **Mode**: PLAN_AND_CREATE | EXECUTE | REFACTOR | DEBUG | CODE_REVIEW
- **Role**: ARCH | BACKEND | FRONTEND | UX_UI | QA | REVIEWER

### Step 4: Store Context in Memory

Use `user-memory-create_entities` to store:
- Task summary
- Identified files to modify
- Key constraints
- Dependencies

### Step 5: Research

If there are new implmntation or new libraries to use:
- Use `user-perplexity-ask-perplexity_ask` for general research and new implementations
- Use `user-context7-query-docs` for library documentation

### Step 6: Memory Sync

Check for previous session data:
1. Read `.cursor/hooks/session-for-memory.json` if exists
2. Use `user-memory-create_entities` to store session
3. Use `user-memory-add_observations` for learnings
4. Delete the file after processing

Load relevant memories:
1. Use `user-memory-search_nodes` for related past work
2. Include relevant context in current task

## Required Output Format

```
## Pre-Flight Checklist ✓

### Task Summary
{brief description of what needs to be done}

### Context Loaded
- [x] ARCHITECTURE.md - {1-line summary}
- [x] CONTRACTS.md - {1-line summary}  
- [x] TESTING.md - {1-line summary}
- [x] Service CLAUDE.md - {if applicable}

### Task Breakdown (via sequential-thinking)
1. {step 1}
2. {step 2}
3. {step 3}
...

### Mode & Role
- **Mode**: {detected mode}
- **Role**: {detected role}
- **Rule File**: {corresponding .mdc file to follow}

### Key Constraints
- {constraint 1}
- {constraint 2}

### Dependencies
- {any external dependencies or blockers}

### Memory
- {session id} - {session name}

### Ready to Proceed ✓
```

## Composability

This skill can be chained with:
- `@load-context {path}` - for service, package, or infrastructure context
- `@tdd-setup {feature}` - to set up tests before implementation
- `@research {topic}` - if unknowns were identified

## Example Usage

```
User: @preflight I need to add rate limiting to the API gateway

AI: [Executes sequential-thinking]
    [Reads ARCHITECTURE.md, CONTRACTS.md, TESTING.md]
    [Reads services/api/CLAUDE.md]
    [Stores context in memory]
    [Outputs pre-flight checklist]
```
