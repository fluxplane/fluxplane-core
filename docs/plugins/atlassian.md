# Atlassian Auth

Jira and Confluence support three auth shapes. The names are intentionally
about how requests are authenticated, not only where the token was created.

## Scoped Access Token

Use `auth.method: token` for Atlassian scoped access tokens that call the
Atlassian cloud gateway:

```yaml
- kind: jira
  config:
    cloud_id: cloud-example-1
    auth:
      method: token
      token_env: JIRA_API_TOKEN
- kind: confluence
  config:
    cloud_id: cloud-example-1
    auth:
      method: token
      token_env: CONFLUENCE_API_TOKEN
```

Runtime shape:

- Header: `Authorization: Bearer <token>`
- Base URL: `https://api.atlassian.com/ex/{jira|confluence}/{cloud_id}/...`
- Required config: token environment variable and `cloud_id`

This is the shape used by existing service-account token deployments.

## Basic API Token

Use `auth.method: api_token` for classic account API tokens that authenticate
with an account email and token:

```fish
set -x ATLASSIAN_EMAIL user@example.invalid
set -x ATLASSIAN_API_TOKEN ...
set -x ATLASSIAN_SITE_URL https://example.atlassian.invalid
```

Runtime shape:

- Header: `Authorization: Basic base64(email:token)`
- Base URL: `site_url` or `base_url`
- Required config: email, token, and either `site_url` or `base_url`

`cloud_id` is useful metadata, but it is not enough for this method because
Basic API-token requests use the site REST endpoint rather than the Atlassian
cloud gateway.

## OAuth2

Use `auth.method: oauth2` for the managed OAuth authorization-code flow.
OAuth2 stores and refreshes an access token, then calls the Atlassian cloud
gateway with Bearer auth and `cloud_id`.

## Local Dev Notes

If a personal scoped token returns 401 from
`https://api.atlassian.com/oauth/token/accessible-resources`, the credential is
not being accepted before product permissions are evaluated. Check that the
token was fully created, copied after confirmation, and belongs to the account
you expect.

Domain verification affects which accounts and token policies the organization
can manage. Without a verified domain, an admin may be able to create personal
account tokens but still lack the organization-level controls needed to manage
token policy and account ownership for the cloud site. A dedicated service
account with scoped tokens is the most predictable local-development setup.
