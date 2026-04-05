---
name: research
description: Research a topic using Perplexity and Context7. Gathers information, documentation, and best practices before implementation.
---

# Research Skill

When invoked with `@research {topic}`, conduct thorough research using available MCP tools and store findings for reference.

## Purpose

Ensures informed decision-making by researching topics before implementation. Combines web search, documentation lookup, and memory storage.

## Research Sources

| Source | Tool | Best For |
|--------|------|----------|
| Web Search | `user-perplexity-ask-perplexity_ask` | General questions, best practices, comparisons |
| Documentation | `user-context7-query-docs` | Library APIs, framework patterns |
| Architecture | `user-zen-thinkdeep` | Design decisions, trade-offs |

## Automatic Actions

### Step 1: Understand the Question

Parse the research topic to identify:
- Primary question
- Related sub-questions
- Context (what service/feature is this for)

### Step 2: Search Perplexity

Use `user-perplexity-ask-perplexity_ask` for:
- General best practices
- Common patterns and anti-patterns
- Recent developments (2024-2026)
- Comparisons of approaches

### Step 3: Query Documentation

If the topic involves a library/framework:

1. Use `user-context7-resolve-library-id` to find the library
2. Use `user-context7-query-docs` to get specific documentation

### Step 4: Deep Analysis (if needed)

For architectural decisions, use `user-zen-thinkdeep` to:
- Analyze trade-offs
- Consider project constraints
- Recommend approach

### Step 5: Store Findings

Use `user-memory-create_entities` to store:
- Research topic
- Key findings
- Recommendations
- Sources

## Required Output Format

```
## Research: {topic}

### Question
{Clear statement of what we're researching}

### Context
{Why this research is needed, what service/feature}

---

### Findings

#### From Perplexity (Web Search)
{Summary of web search findings}

**Key Points**:
1. {Point 1}
2. {Point 2}
3. {Point 3}

**Best Practices**:
- {Practice 1}
- {Practice 2}

**Common Pitfalls**:
- {Pitfall 1}
- {Pitfall 2}

#### From Documentation (Context7)
{Library/framework documentation findings}

**API Reference**:
```{language}
{relevant code examples}
```

**Version Notes**:
- Using version: {version}
- Relevant since: {version where feature was introduced}

#### Deep Analysis (Zen)
{Architectural analysis if applicable}

**Trade-offs**:
| Option | Pros | Cons |
|--------|------|------|
| {Option 1} | {pros} | {cons} |
| {Option 2} | {pros} | {cons} |

---

### Recommendations

**Recommended Approach**: {approach}

**Reasoning**:
{Why this approach is best for our project}

**Implementation Notes**:
1. {Note 1}
2. {Note 2}

---

### Sources
1. {Source 1 - URL or doc reference}
2. {Source 2}
3. {Source 3}

### Stored in Memory ✓
Entity: "{topic}" research findings
```

## Research Templates

### For Library Selection

```
@research best library for {use case} in {language/framework}

Questions to answer:
1. What are the options?
2. Which is most maintained/popular?
3. Which fits our stack best?
4. What are the trade-offs?
```

### For Implementation Pattern

```
@research how to implement {feature} in {framework}

Questions to answer:
1. What's the recommended approach?
2. Are there examples in our codebase?
3. What are common mistakes?
4. What testing approach is best?
```

### For Problem Solving

```
@research why {problem} happens in {context}

Questions to answer:
1. What causes this issue?
2. What are common solutions?
3. How do others handle this?
4. What's the best fix for our case?
```

## Composability

This skill works well with:
- Before `@preflight` - research before starting
- During `@debug-session` - research unfamiliar issues
- Before `@scaffold-module` - research patterns to use

## Example Usage

```
User: @research best practices for rate limiting in NestJS APIs

AI: [Queries Perplexity for rate limiting best practices]
    [Finds @nestjs/throttler library]
    [Queries Context7 for @nestjs/throttler documentation]
    [Uses zen-thinkdeep for our specific needs]
    [Stores findings in memory]
    [Outputs comprehensive research summary]
```

## Research Ethics

- Always cite sources
- Prefer official documentation over blog posts
- Note version compatibility
- Flag if information might be outdated
