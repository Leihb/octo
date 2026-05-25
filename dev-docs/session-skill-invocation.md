# Session + Skill Invocation Pattern

> Design pattern for launching an Agent session that immediately runs a skill.
> Follow this whenever a UI action needs to "open a session and do something automatically."

---

## The Pattern

```
1. POST /api/sessions          → create a named session
2. Sessions.add(session)       → register locally
3. Sessions.renderList()       → update sidebar
4. _bootUI() if needed         → connect WS (only on first boot)
5. Sessions.select(session.id) → navigate to session (triggers WS subscribe)
6. WS.send({ type: "message", session_id, content: "/skill-name" })
                               → agent runs the skill immediately
```

The slash command (`/skill-name`) is handled by `Agent#parse_skill_command` on the
server side — no special API endpoint or pending-state machinery needed.

---

## Real Usages

### Create Task (`tasks.js → createInSession`)
```js
Sessions.select(session.id);
WS.send({ type: "message", session_id: session.id, content: "/create-task" });
```

### Onboard (`onboard.js → _startSoulSession`)
```js
_bootUI();                  // WS.connect() + Tasks/Skills load
Sessions.add(session);
Sessions.renderList();
Sessions.select(session.id);
WS.send({ type: "message", session_id: session.id, content: "/onboard" });
```

---

## When to Use `pending_task` Instead

Use the `pending_task` registry field (and the `run_task` WS message) **only** when
the prompt is a large block of text read from a file (e.g. `POST /api/tasks/run`).

For slash commands, always prefer the direct `WS.send` approach above — simpler and
no server-side state to manage.

---

## Anti-patterns Avoided

| Anti-pattern | Why it was wrong |
|---|---|
| Store `_pendingSessionId` in module state, resolve on `session_list` | Race condition between WS connect and session_list arrival; unnecessary complexity |
| Custom `takePendingSession()` hook in app.js `session_list` handler | Spread logic across files; hard to trace |
| Send prompt via `setTimeout` after boot | Fragile timing; breaks if WS is slow |

---

## Key Insight

`Sessions.select(id)` triggers a WS `subscribe` message. Once the server confirms
with `subscribed`, the client is guaranteed to receive all subsequent broadcasts for
that session. Sending `WS.send({ type: "message" })` right after `select` is safe
because the WebSocket driver queues messages until the connection is open.
