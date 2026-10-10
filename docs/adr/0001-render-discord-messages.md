# Winnow renders Discord messages instead of forwarding to /github

Discord has a `/github` endpoint. It accepts GitHub webhook payloads and makes its own messages. Winnow does not use it. Winnow makes its own message for each routed Event. (The first version made an embed. Superseded by [0010](0010-render-components-v2-messages.md): winnow now sends Components V2 messages.)

The `/github` endpoint renders only 18 GitHub events. It drops all security events with HTTP 204 and makes no message, so a 204 does not prove that a message exists. Forgejo has no such endpoint. Winnow needs Renderers for Forgejo and for security events. One parser reads both forges, so these Renderers also cover the GitHub events.

## Considered options

- Forward the events that Discord renders, and render only the rest. Rejected because it gives two delivery paths, and the silent drops stay.
- Forward Forgejo payloads to `/github` with a changed event header. Rejected because nobody knows if Discord renders them correctly, and security events still need Renderers.

## Consequences

Each event type that winnow shows well needs a Renderer and a fixture test. An event type with no Renderer gets a Fallback message.
