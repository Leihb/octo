# Agent-First UI Design Philosophy

> Guiding principle for all Octo UI and feature design.

---

## Core Principle

**Conversation first, interactive cards when needed.**

Users interact with the Agent through natural language to accomplish everything. When conversation is inconvenient for structured input (e.g. dropdowns, multi-select, precise time picking), the Agent triggers an **interactive card** via the `request_user_feedback` tool — rendered by the frontend as a structured UI component. Cards are a complement to conversation, not a replacement.

---

## Two Interaction Modes

### 1. Conversation (default)
User expresses intent in natural language, Agent understands and executes.

```
User:  Send me a daily standup summary every morning at 9
Agent: Done! Task created, runs Mon–Fri at 09:00 ✅
```

### 2. Interactive Cards (when conversation falls short)
When the Agent needs structured input that's hard to express in free text, it calls `request_user_feedback`. The frontend renders this as an interactive card (dropdowns, radio buttons, time pickers, etc.).

```
Agent calls request_user_feedback → frontend renders a card:

┌─────────────────────────────┐
│ 📋 Confirm task settings     │
│ Frequency: [Daily      ▼]   │
│ Time:      [09:00      ]    │
│            [✅ Confirm] [Cancel] │
└─────────────────────────────┘

User fills card → structured data sent back to Agent → execution continues
```

---

## When to Use Cards

| Situation | Reason |
|-----------|--------|
| Choosing from a list of options | Easier than enumerating in chat |
| Date / time selection | Precise value, error-prone in free text |
| Sensitive input like API keys | Should not appear in conversation history |
| Collecting multiple fields at once | One card beats several back-and-forth questions |

Everything else: use conversation.

---

## What Should NOT Exist

- ❌ Persistent configuration form pages
- ❌ Fields that require users to understand technical details (cron expressions, agent IDs, etc.)
- ❌ More than 3 action buttons per list row
- ❌ Standalone "Create" form modals

---

## Role of UI Pages

UI pages are for **displaying state**, not for configuring things:

- ✅ Show task lists, run history, current status
- ✅ Minimal action set per row: ▶ Run / ✎ Edit (opens conversation) / ✕ Delete
- ❌ No inline create/edit forms inside list pages

Clicking "Edit" opens an Agent conversation with context pre-filled. The Agent drives the modification flow from there.

---

*Applies to all Octo Web UI and feature design.*
