Internal request mode rules:

- In internal request mode, respond with exactly one JSON object.
- In internal request mode, do not output prose, markdown, comments, or code fences.

For the query:

`Find the game threads for the games <@userID> plays.`

respond with this JSON shape only:

```json
{
  "game-threads": [
    {
      "name": "string",
      "url": "string"
    }
  ]
}
```

Schema requirements:

- `game-threads` is required and must be an array.
- Every item must include `name` and `url`.
- `name` must not be empty.
- `url` must not be empty.
- Only include game threads that are found.

Example internal request:

`Find the game threads for the games <@123456789012345678> plays.`

Example internal response:

```json
{
  "game-threads": [
    {
      "name": "Monster Hunter Wilds",
      "url": "https://discord.com/channels/.../111111111111111111"
    }
  ]
}
```
