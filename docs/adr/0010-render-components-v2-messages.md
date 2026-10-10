# Winnow sends Components V2 messages instead of embeds

Winnow sends each message as a Discord Components V2 message: Event messages, Digests, Sweep alerts, and Fallback messages. A Components V2 message can show link buttons, for example "Diff" and "Checks" on a pull request, and it gives the body more room beside the avatar. Winnow sends only link buttons, because a channel webhook that no application owns cannot send buttons that call winnow back. Thus winnow needs no Discord bot application. Discord counts the text of all components in one message together, with a limit of 4000 characters. Winnow cuts the body to the part of this limit that the rest of the message leaves.

## Considered options

- Keep the embeds. Rejected because an embed cannot show buttons.
- A format setting for each Sink, with embeds or Components V2. Rejected because each Renderer then needs two layouts, and no operator needs the embeds.

## Consequences

Winnow adds `with_components=true` to each Sink URL and keeps the query that the URL has, for example `thread_id`. Discord refuses a Components V2 message without this parameter. Discord also refuses `content` and `embeds` in such a message, so the Ping is a Text Display above the message box.
