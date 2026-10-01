---
title: Bots
---

# Bots: alerts and commands from a chat

## Telegram

The Telegram bot sends hub alerts with buttons and takes commands from a
chat. **The hub reaches Telegram by itself**: it sends messages and picks
up commands and button presses by long polling (`getUpdates`). So the bot
needs neither an edge nor an open port, and it works with a hub behind
NAT.

### Setup

1. With [@BotFather](https://t.me/BotFather), use `/newbot` and get the
   token (`123456789:AA…`).
2. "Alerts" → **"Telegram bot"** → "Configure": paste the token; the hub
   checks it with Telegram and shows the bot name.
3. Send the bot `/start` in the chat you need (add the bot to a group
   first): it answers with the chat number. Add the chat in the settings:
   - **role**: *read* gets state and view buttons; *admin* also gets
     deploys, dry runs, bans and unbans;
   - **alerts**: whether this chat receives events.
4. **Who may act**: the Telegram user ids of people (the bot tells you
   yours on `/id`). Empty means any member of a chat with the admin role;
   for groups, set the list.
5. **Alerts**: which events to send (nothing ticked means all), and the
   text **language**.

Edits happen in a window with a "what will change" diff, and everything
goes to the audit log. A chat's "Test message" checks that the bot can
write there.

### Commands

| Command | What | Chat role |
|---|---|---|
| `/status` | how many hosts are up, findings by severity | read |
| `/hosts` | hosts: up or not, findings | read |
| `/alerts` | the latest alerts | read |
| `/pipelines` | pipelines and the last deployment outcome | read |
| `/deploy <pipeline>` | deploy, after the "Deploy" button | admin |
| `/dryrun <pipeline>` | a dry run with the checks chosen on the pipeline | admin |
| `/ban <IP>`, `/unban <IP>` | ban and unban on every host with fail2ban, after the button | admin |
| `/id` | the chat number and yours | any |

A pipeline is given by name or number. The bot sends the outcome of the
job it started by itself (the status and the last log lines).

### Buttons in alerts

- a host alert has **"Overview"** (state, version, findings); a new
  findings alert also has **"Findings"** (the ten most important);
- a deployment has **"Log"** (the last lines); a failed one also has
  **"Retry"** (with confirmation, admin role).

### Security

- The bot answers other chats only with their number on `/start` and
  `/id`.
- Actions happen only in chats with the admin role, from people on the
  list (if one is set) and only after the confirmation button; a
  confirmation lives 10 minutes and works only for whoever ran the
  command.
- Actions run as `telegram:<name>` and go to the audit log.
- The bot token is stored encrypted with the hub key and is never shown
  in error logs.
