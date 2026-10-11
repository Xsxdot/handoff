# Handoff Privacy Policy

Effective date: October 11, 2026

This policy describes how Handoff handles information when you use its CLI,
desktop or mobile clients, agent daemon (`agentd`), and optional Handoff cloud
relay and account console. The Handoff project is maintained by
[Xsxdot](https://github.com/Xsxdot). The public version of this policy is at
[github.com/Xsxdot/handoff/blob/main/PRIVACY.md](https://github.com/Xsxdot/handoff/blob/main/PRIVACY.md).

## 1. Local use and direct connections

Handoff stores configuration, authentication information, task plans, prompts,
messages, approval decisions, execution output, project and repository metadata,
and diagnostic logs on the machines where Handoff and its agents run. Depending
on your configuration, collaboration records may also be stored in a database
you select, such as PostgreSQL. These records can contain personal information
or confidential project content that you or an agent supply.

For local-only coordination, Handoff communicates with your local agent daemon.
For a direct remote connection, information needed for the requested operation
leaves your device and is sent to the executor or other Handoff machine you
select. This can include task instructions, project paths, messages, commands,
approval responses, logs, file contents, repository bundles, and results.
Repositories and changes may also be transferred through your configured Git
host. The receiving machine can read and retain this information.

Direct coordination does not send this task traffic through the Handoff cloud
relay. It does not disable separate network requests made by AI executors,
Git, update checks, optional notifications, or other services you configure.
Direct agent connections use HTTP/WebSocket without built-in TLS; use a trusted
network or a secure network overlay. The relay encryption described below does
not apply to direct connections.

## 2. Optional cloud relay

When you configure a cloud relay, Handoff sends relay registration or connection
credentials and the target node name to that relay. Its operator processes
account and credential identifiers, node names, source IP addresses, connection
times, connection events and errors, online status, and traffic volumes. The
relay also observes connection timing and encrypted traffic sizes. This metadata
is used to authenticate and route connections, enforce quotas and rate limits,
measure usage, and diagnose or prevent abuse.

Handoff encrypts tunnel contents end to end between the participating Handoff
clients and agent daemon, using keys derived from the executor's Handoff token.
The relay receives separate relay credentials; it does not receive the executor
token or the keys needed to decrypt tunnel contents. The standard relay forwards
encrypted tunnel bytes through transient memory buffers and does not persist
the tunnel payloads. Account, usage, and connection metadata are stored
separately and are not covered by this end-to-end encryption.

The intended recipient can decrypt and store the transmitted content. Tunnel
encryption also does not cover separate account-console requests, email, push
notifications, or requests an executor sends to an AI provider. Production relay
connections should use `wss://` to protect the outer transport and relay
credentials.

## 3. Relay accounts, website, and email

If you create a relay account, the service processes your email address and
password to register, verify, and authenticate you. The database stores a
password hash, account status and timestamps, hashed verification/reset tokens,
and hashed login-session tokens. Session records include IP addresses and browser
user-agent information. The service also stores node names, credential hashes
and labels, quota or subscription allocations, traffic-pack balances, usage
samples, notification preferences, notification event records, and administrative
audit records. Application logs may include email addresses, IP addresses,
account or node identifiers, timestamps, and errors.

The console uses essential login cookies and browser storage for its operation
and preferences. The standard Handoff clients and console do not include
advertising trackers or a separate product-analytics SDK.

The standard server integrates with **Resend** for verification, password-reset,
and service-notification email. When that integration is used, Resend receives
the recipient's email address and the email contents, including verification or
reset links and relevant service information. Your email provider also processes
the delivered email. An explicitly configured development email mode writes
identity links to local server logs instead of sending email; those logs must
be treated as sensitive.

## 4. Mobile pairing and optional iOS notifications

Mobile clients keep pairing credentials and selected-machine information on
your device and send authentication and operation requests to the selected
Handoff machine. Camera access is used to scan pairing QR codes. Handoff does
not upload the camera image as part of pairing; the decoded connection details
are used to establish the connection.

If you grant iOS notification permission and the connected agent daemon is
configured to send push notifications, the app registers a device identifier
and Apple Push Notification service (APNs) token with that daemon. The daemon
stores that registration, associated member/session information, and an update
timestamp. To deliver notifications, it sends the APNs token and notification
payload to **Apple**. Payloads include a notification title, event type, member
identifier, card/reference identifiers, navigation link, and badge count.
Titles can include an excerpt of a message, question, or approval request.
These notification contents are separate from the encrypted relay tunnel and
may appear on your device's lock screen according to your system settings.

You can deny or disable notifications in iOS Settings. Disabling permission
does not by itself erase a registration already stored on the daemon. The
daemon supports removal of device registrations and removes tokens reported
as unregistered by APNs.

## 5. Other recipients and purposes

Handoff uses information to perform the operations you request, authenticate
users and machines, deliver service messages, maintain usage limits, and
operate and secure the service. Handoff does not sell personal information
or use task contents for advertising.

Other recipients depend on the features and services you use:

- **AI executors and model providers:** executors you select, such as Claude
  Code, Codex, OpenCode, or Grok, may send prompts, source code, files, execution
  output, and conversation context to their configured providers. Those tools
  and providers have their own privacy and retention practices.
- **Git and release hosting:** repository operations, release checks, and
  downloads contact Git hosts, GitHub, or configured download endpoints. These
  services receive the request and ordinary connection metadata, such as your
  IP address, requested resource, and HTTP headers.
- **Infrastructure and network providers:** the operator of a relay or database
  you select, hosting providers, reverse proxies, and network-overlay providers
  process the data required to provide their services. Infrastructure logs may
  retain request paths, IP addresses, and timestamps. Email and push delivery
  involve Resend, email providers, and Apple as described above.
- **Support and required disclosures:** information you choose to provide in
  support requests is received by the maintainer and the support platform.
  Information may also be disclosed when legally required or necessary to
  investigate abuse and protect the service.

Information may be processed in the locations where your selected machines,
relay operator, and providers operate. Self-hosted operators are responsible
for their deployment's additional logging, backups, recipients, and policies.

## 6. Retention and deletion

- **Local and executor data:** task records, files, credentials, and logs remain
  on the relevant machines or configured database until removed by you, the
  machine/database operator, or an applicable cleanup operation. Uninstalling a
  client does not necessarily delete daemon data, remote records, OS credential
  storage, or backups. Handoff's task-cache garbage collection is not a complete
  deletion of task history or collaboration records.
- **Relay payloads:** the standard relay does not write tunnel contents to
  persistent storage. Transient buffers are used while forwarding traffic.
- **Relay connection and notification-event records:** the standard server
  defaults to a seven-day age threshold (`conn_log_ttl`). Cleanup runs at startup
  and periodically at that interval, deleting records older than the threshold.
  This is not an exact seven-day deletion deadline: records can remain until
  the next successful cleanup, and operators can change the interval.
- **Accounts and other server records:** account information, credential hashes,
  quota and usage records, preferences, session/token records, and administrative
  audit records have no universal automatic deletion period in the current
  server. Verification links normally expire after 24 hours, password-reset
  links after one hour, and console sessions after 12 hours by default. Expiry
  prevents use of a token or session; it does not guarantee erasure of its
  database record. Logout deletes the corresponding console session; credential
  revocation prevents future use but can retain the credential record.
- **Push registrations:** registrations remain on the connected daemon until
  removed or invalidated as described above. Disabling notifications or
  uninstalling the app does not guarantee immediate server-side deletion.
- **Infrastructure logs, backups, and third parties:** these are separate from
  the relay's connection-log cleanup. Their retention depends on the operator
  or provider; Handoff does not impose a common deletion deadline on them.

For a project-operated relay account, contact the maintainer using the process
below to request access, correction, or deletion of personal information. The
current console does not provide a self-service account-deletion button.
Requests require verification of account ownership. Records required for legal
obligations or resolving abuse may need to be retained; deletion from active
storage does not necessarily remove existing backups immediately. For a
self-hosted or third-party relay, contact its operator. Requests concerning an
AI, email, hosting, or other provider must also follow that provider's process.

## 7. Your choices and contact

You can use local/direct coordination without creating a Handoff relay account,
choose your executor and network services, stop using a relay, revoke relay
credentials, and adjust service-email preferences in the console. You can manage
camera and notification permissions in your device's settings.

For privacy questions or to start an access, correction, or deletion request,
contact the maintainer through the
[Handoff issue tracker](https://github.com/Xsxdot/handoff/issues), identifying
the request as a privacy inquiry. **Do not post passwords, tokens, personal
account details, private code, or logs containing sensitive information in a
public issue.** Ask for a private communication channel before providing
information needed to verify ownership or process the request. For services
run by another operator, use that operator's privacy contact.

## 8. Changes to this policy

Updates will be published at the public policy URL above with a revised
effective date. The repository history records previous versions. Information
handling can depend on your installed version and enabled integrations; sections
about optional features apply when you use those features.
