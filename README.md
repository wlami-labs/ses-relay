# ses-relay

A small SMTP server that sends every message it receives through the
[Amazon SES v2 API](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_SendEmail.html).

Built for apps that only speak SMTP (here: [Listmonk](https://listmonk.app))
but should use SES **without SMTP passwords**. The relay uses the AWS SDK's
default credential chain, so on Kubernetes it gets short-lived credentials
from the pod's projected service-account token (`AssumeRoleWithWebIdentity`).
No access key is stored anywhere.

```
app ──SMTP 127.0.0.1:2525──▶ ses-relay ──sesv2.SendEmail (raw)──▶ Amazon SES
```

- Messages are passed through **unchanged** (raw MIME), so headers like
  `List-Unsubscribe` / `List-Unsubscribe-Post` reach the recipient.
- The SMTP envelope recipients become the SES destination (Bcc works).
- Every message is sent with the configured SES configuration set, so SES
  event destinations (bounces, complaints) apply.
- Distroless image, about 13 MB, a few MiB of RAM.

## Security model

The relay has **no SMTP authentication**. It must only be reachable by the
app it serves: run it as a sidecar listening on loopback (the default
`127.0.0.1:2525`). Never expose the port through a Service.

## Configuration

| Env | Default | |
|---|---|---|
| `LISTEN_ADDR` | `127.0.0.1:2525` | SMTP listen address |
| `SES_CONFIGURATION_SET` | (required) | SES configuration set for every message |
| `AWS_REGION` | | SES region, e.g. `eu-central-1` |
| `AWS_ROLE_ARN`, `AWS_WEB_IDENTITY_TOKEN_FILE` | | web identity credentials (any AWS SDK credential source works) |

Limits: 10 MiB per message, 50 recipients per message.

Flag `-healthcheck`: opens an SMTP session to `LISTEN_ADDR` (greeting, QUIT) and exits 0/1. Use it as an
exec probe (the image has no shell).

## SMTP replies

| SES result | SMTP reply | Client behaviour |
|---|---|---|
| sent | `250` | done |
| `TooManyRequestsException`, `LimitExceededException`, `SendingPausedException`, network or credential errors | `451 4.3.0` | retry later |
| any other SES API error (e.g. `MessageRejected`, unverified identity) | `554 5.6.0` | permanent failure |
| message over 10 MiB | `552` | permanent failure |

## IAM

The role needs only:

```json
{
  "Effect": "Allow",
  "Action": ["ses:SendRawEmail", "ses:SendEmail"],
  "Resource": [
    "arn:aws:ses:<region>:<account>:identity/<your-domain>",
    "arn:aws:ses:<region>:<account>:configuration-set/<your-set>"
  ]
}
```

## Kubernetes example

Native sidecar (Kubernetes 1.29+), so the relay is up before the app starts:

```yaml
spec:
  serviceAccountName: listmonk
  initContainers:
    - name: ses-relay
      image: ghcr.io/wlami-labs/ses-relay:0.1.1
      restartPolicy: Always
      env:
        - { name: SES_CONFIGURATION_SET, value: listmonk }
        - { name: AWS_REGION, value: eu-central-1 }
        - { name: AWS_ROLE_ARN, value: arn:aws:iam::<account>:role/listmonk-ses-relay }
        - { name: AWS_WEB_IDENTITY_TOKEN_FILE, value: /var/run/secrets/aws/token }
      volumeMounts:
        - { name: aws-token, mountPath: /var/run/secrets/aws, readOnly: true }
      startupProbe:
        exec: { command: ["/app", "-healthcheck"] }
      resources:
        requests: { memory: 16Mi, cpu: 10m }
        limits: { memory: 64Mi }
  containers:
    - name: app
      # SMTP host 127.0.0.1, port 2525, no auth, no TLS
  volumes:
    - name: aws-token
      projected:
        sources:
          - serviceAccountToken:
              audience: sts.amazonaws.com
              expirationSeconds: 3600
              path: token
```

The cluster's service-account issuer must be registered as an IAM OIDC
provider, and the role's trust policy must allow
`system:serviceaccount:<namespace>:<serviceaccount>` with audience
`sts.amazonaws.com`.

## Development

```bash
go test ./...
go vet ./...
docker build -t ses-relay .
```

## Releases

Push a tag `vX.Y.Z`. The release workflow runs the tests, builds a
`linux/arm64` + `linux/amd64` image and publishes it to
`ghcr.io/wlami-labs/ses-relay` (private package) with the tags `X.Y.Z`,
`X.Y`, `latest`, then creates a GitHub release.

```bash
git tag v0.1.0 && git push origin v0.1.0
```
