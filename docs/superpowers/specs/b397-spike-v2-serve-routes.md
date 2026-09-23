| `GET /api/agent` | `agent.list` | List agents |
| `GET /api/agent/{agentID}` | `agent.get` | Get agent |
| `GET /api/command` | `command.list` | List commands |
| `GET /api/config` | `config.get` | Get configuration |
| `GET /api/config/shell` | `config.shells` | List available shells |
| `PATCH /api/credential/{credentialID}` | `credential.update` | Update credential |
| `DELETE /api/credential/{credentialID}` | `credential.remove` | Remove credential |
| `POST /api/credential/{credentialID}/activate` | `credential.activate` | Activate credential |
| `GET /api/debug/location` | `debug.location.list` | List loaded locations |
| `DELETE /api/debug/location` | `debug.location.evict` | Evict a loaded location |
| `GET /api/event` | `event.subscribe` | Subscribe to events |
| `PATCH /api/experimental/config` | `experimental.config.update` | Update global configuration |
| `POST /api/experimental/fs/write` | `experimental.fs.write` | Write file |
| `POST /api/experimental/generate` | `experimental.generate.text` | Generate text |
| `POST /api/experimental/integration/wellknown` | `experimental.integration.wellknown.add` | Add wellknown integration |
| `PUT /api/experimental/mcp/{server}` | `experimental.mcp.add` | Add MCP server |
| `DELETE /api/experimental/mcp/{server}` | `experimental.mcp.remove` | Remove MCP server |
| `POST /api/experimental/mcp/{server}/connect` | `experimental.mcp.connect` | Connect MCP server |
| `POST /api/experimental/mcp/{server}/disconnect` | `experimental.mcp.disconnect` | Disconnect MCP server |
| `GET /api/experimental/migration/v1` | `experimental.migration.v1.status` | Get V1 migration status |
| `POST /api/experimental/persistent-pty/handoff` | `server.experimental.persistentPty.handoff` |  |
| `POST /api/experimental/persistent-pty/shutdown` | `server.experimental.persistentPty.shutdown` |  |
| `GET /api/experimental/persistent-pty/{ptyID}` | `server.experimental.persistentPty.get` |  |
| `PUT /api/experimental/persistent-pty/{ptyID}` | `server.experimental.persistentPty.update` |  |
| `DELETE /api/experimental/persistent-pty/{ptyID}` | `server.experimental.persistentPty.remove` |  |
| `GET /api/experimental/persistent-pty/{ptyID}/connect` | `persistentPty.connect` | Connect to a persistent PTY |
| `POST /api/experimental/persistent-pty/{ptyID}/connect-token` | `server.experimental.persistentPty.connectToken` |  |
| `GET /api/experimental/persistent-pty/{ptyID}/snapshot` | `server.experimental.persistentPty.snapshot` |  |
| `POST /api/experimental/session/import` | `experimental.session.import` | Import session |
| `GET /api/experimental/session/stats` | `experimental.session.stats` | Get session statistics |
| `GET /api/experimental/session/{sessionID}/export` | `experimental.session.export` | Export session |
| `GET /api/experimental/session/{sessionID}/instructions/entries` | `experimental.session.instructions.entry.list` | List instruction entries |
| `PUT /api/experimental/session/{sessionID}/instructions/entries/{key}` | `experimental.session.instructions.entry.put` | Put instruction entry |
| `DELETE /api/experimental/session/{sessionID}/instructions/entries/{key}` | `experimental.session.instructions.entry.remove` | Remove instruction entry |
| `GET /api/experimental/session/{sessionID}/log` | `session.log` | Read the session log |
| `POST /api/experimental/session/{sessionID}/skill` | `experimental.session.skill` | Activate skill |
| `GET /api/experimental/session/{sessionID}/terminal` | `server.experimental.persistentPty.list` |  |
| `POST /api/experimental/session/{sessionID}/terminal` | `server.experimental.persistentPty.create` |  |
| `GET /api/experimental/session/{sessionID}/terminal/read` | `server.experimental.persistentPty.read` | Read the session's most recently controlled terminal |
| `POST /api/experimental/session/{sessionID}/wait` | `experimental.session.wait` | Wait for session |
| `GET /api/form` | `form.list` | List pending forms |
| `GET /api/fs/find` | `fs.find` | Find files |
| `GET /api/fs/list` | `fs.list` | List directory |
| `GET /api/fs/read/*` | `fs.read` | Read file |
| `GET /api/info` | `server.info` | Get server info |
| `GET /api/integration` | `integration.list` | List integrations |
| `GET /api/integration/{integrationID}` | `integration.get` | Get integration |
| `POST /api/integration/{integrationID}/connect/command` | `integration.command.connect` | Begin command connection |
| `GET /api/integration/{integrationID}/connect/command/{attemptID}` | `integration.command.status` | Get command attempt status |
| `DELETE /api/integration/{integrationID}/connect/command/{attemptID}` | `integration.command.cancel` | Cancel command connection |
| `POST /api/integration/{integrationID}/connect/key` | `integration.connect.key` | Connect with key |
| `POST /api/integration/{integrationID}/connect/oauth` | `integration.oauth.connect` | Begin OAuth connection |
| `GET /api/integration/{integrationID}/connect/oauth/{attemptID}` | `integration.oauth.status` | Get OAuth attempt status |
| `DELETE /api/integration/{integrationID}/connect/oauth/{attemptID}` | `integration.oauth.cancel` | Cancel OAuth connection |
| `POST /api/integration/{integrationID}/connect/oauth/{attemptID}/complete` | `integration.oauth.complete` | Complete OAuth connection |
| `GET /api/location` | `location.get` | Get location |
| `POST /api/location/reload` | `location.reload` | Reload configuration |
| `GET /api/mcp` | `mcp.list` | List MCP servers |
| `GET /api/mcp/resource` | `mcp.resource.catalog` | List MCP resources |
| `GET /api/model` | `model.list` | List models |
| `GET /api/model/default` | `model.default` | Get default model |
| `GET /api/permission/request` | `permission.request.list` | List pending permission requests |
| `GET /api/permission/saved` | `permission.saved.list` | List saved permissions |
| `DELETE /api/permission/saved/{id}` | `permission.saved.remove` | Remove saved permission |
| `GET /api/plugin` | `plugin.list` | List plugins |
| `POST /api/plugin/check` | `plugin.check` | Check plugin updates |
| `POST /api/plugin/update` | `plugin.update` | Update plugins |
| `GET /api/project` | `project.list` | List projects |
| `PATCH /api/project/{projectID}` | `project.update` | Update project |
| `GET /api/provider` | `provider.list` | List providers |
| `GET /api/provider/{providerID}` | `provider.get` | Get provider |
| `GET /api/pty` | `pty.list` | List PTY sessions |
| `POST /api/pty` | `pty.create` | Create PTY session |
| `GET /api/pty/{ptyID}` | `pty.get` | Get PTY session |
| `PUT /api/pty/{ptyID}` | `pty.update` | Update PTY session |
| `DELETE /api/pty/{ptyID}` | `pty.remove` | Remove PTY session |
| `GET /api/pty/{ptyID}/connect` | `pty.connect` | Connect to PTY session |
| `POST /api/pty/{ptyID}/connect-token` | `pty.connect.token` | Create PTY WebSocket token |
| `GET /api/reference` | `reference.list` | List references |
| `POST /api/rpc/{rpcID}/{method}` | `rpc.call` | Call a plugin RPC |
| `GET /api/session` | `session.list` | List sessions |
| `POST /api/session` | `session.create` | Create session |
| `GET /api/session/active` | `session.active` | List active sessions |
| `GET /api/session/{sessionID}` | `session.get` | Get session |
| `PATCH /api/session/{sessionID}` | `session.update` | Update session |
| `DELETE /api/session/{sessionID}` | `session.remove` | Delete session |
| `POST /api/session/{sessionID}/agent` | `session.switchAgent` | Switch session agent |
| `POST /api/session/{sessionID}/background` | `session.background` | Background blocking session tools |
| `POST /api/session/{sessionID}/command` | `session.command` | Run command |
| `POST /api/session/{sessionID}/compact` | `session.compact` | Compact session |
| `GET /api/session/{sessionID}/context` | `session.context` | Get session context |
| `GET /api/session/{sessionID}/diff` | `session.diff` | Diff session turns |
| `PUT /api/session/{sessionID}/environment` | `session.environment` | Set session environment |
| `POST /api/session/{sessionID}/fork` | `session.fork` | Fork session |
| `GET /api/session/{sessionID}/form` | `session.form.list` | List session forms |
| `POST /api/session/{sessionID}/form` | `session.form.create` | Create session form |
| `GET /api/session/{sessionID}/form/{formID}` | `session.form.get` | Get session form |
| `DELETE /api/session/{sessionID}/form/{formID}` | `session.form.cancel` | Cancel form |
| `POST /api/session/{sessionID}/form/{formID}/reply` | `session.form.reply` | Reply to form |
| `POST /api/session/{sessionID}/generate` | `session.generate` | Generate text from session context |
| `GET /api/session/{sessionID}/inbox` | `session.inbox.list` | List session inbox |
| `PATCH /api/session/{sessionID}/inbox/{inboxID}` | `session.inbox.update` | Update inbox item |
| `DELETE /api/session/{sessionID}/inbox/{inboxID}` | `session.inbox.cancel` | Cancel inbox input |
| `POST /api/session/{sessionID}/interrupt` | `session.interrupt` | Interrupt session execution |
| `GET /api/session/{sessionID}/message` | `session.message.list` | Get session messages |
| `GET /api/session/{sessionID}/message/{messageID}` | `session.message.get` | Get session message |
| `POST /api/session/{sessionID}/model` | `session.switchModel` | Switch session model |
| `POST /api/session/{sessionID}/move` | `session.move` | Move session |
| `GET /api/session/{sessionID}/permission` | `session.permission.list` | List session permission requests |
| `POST /api/session/{sessionID}/permission` | `session.permission.create` | Create permission request |
| `GET /api/session/{sessionID}/permission/{requestID}` | `session.permission.get` | Get permission request |
| `POST /api/session/{sessionID}/permission/{requestID}/reply` | `session.permission.reply` | Reply to pending permission request |
| `POST /api/session/{sessionID}/prompt` | `session.prompt` | Send message |
| `DELETE /api/session/{sessionID}/revert` | `session.revert.clear` | Clear staged revert |
| `POST /api/session/{sessionID}/revert/commit` | `session.revert.commit` | Commit staged revert |
| `POST /api/session/{sessionID}/revert/stage` | `session.revert.stage` | Stage session revert |
| `POST /api/session/{sessionID}/shell` | `session.shell` | Run shell command |
| `POST /api/session/{sessionID}/synthetic` | `session.synthetic` | Add synthetic message |
| `POST /api/session/{sessionID}/view` | `session.view` | View session |
| `GET /api/shell` | `shell.list` | List running shell commands |
| `POST /api/shell` | `shell.create` | Run shell command |
| `GET /api/shell/{id}` | `shell.get` | Get shell command |
| `DELETE /api/shell/{id}` | `shell.remove` | Remove shell command |
| `GET /api/shell/{id}/output` | `shell.output` | Read shell output |
| `GET /api/skill` | `skill.list` | List skills |
| `GET /api/vcs` | `vcs.get` | VCS info |
| `GET /api/vcs/base` | `vcs.base` | VCS review base |
| `GET /api/vcs/branch` | `vcs.branch.list` | VCS branches |
| `GET /api/vcs/diff` | `vcs.diff` | VCS diff |
| `GET /api/vcs/status` | `vcs.status` | VCS status |
| `POST /api/websearch` | `websearch.query` | Search the web |
| `GET /api/websearch/provider` | `websearch.providers` | List web search providers |
| `GET /api/worktree` | `worktree.list` | List worktrees |
| `POST /api/worktree` | `worktree.create` | Create worktree |
| `DELETE /api/worktree` | `worktree.remove` | Remove worktree |
| `POST /api/worktree/refresh` | `worktree.refresh` | Refresh worktrees |
