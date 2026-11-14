---
page_title: "Invitations and members"
subcategory: ""
description: |-
  Invite people, then manage their role once they have accepted.
---

# Invitations and members

A person joins an organisation by accepting an invitation, and no one but the person can do that. Terraform can send the invitation and, once the person has accepted, manage what they are. The two steps are two resources.

## Invite

```terraform
resource "heytownhall_invitation" "ana" {
  email = "ana@example.com"
}
```

An invitation changes state without Terraform. What the resource does in each case:

| What happens | What Terraform shows |
| --- | --- |
| Pending | `status = "pending"`. Destroying it cancels the invitation. |
| Accepted (by the emailed link, or by signing in through single sign-on) | The resource stays in the state with `status = "accepted"` and `accepted_user_id` set. The plan is empty. Destroying it does nothing: the person remains a member. |
| Expired, cancelled in the dashboard, or rejected | The resource leaves the state. The next plan creates a new invitation and sends a new email. |

An accepted invitation does not make a plan that keeps changing, and it does not send a second email.

## Manage the member

`heytownhall_member` sets the role of someone who is already a member, so it can only be created after the invitation is accepted. The accepted invitation names the person (`accepted_user_id`), so the two resources chain directly:

```terraform
resource "heytownhall_member" "ana" {
  user_id = heytownhall_invitation.ana.accepted_user_id
  role    = "member"
}
```

On the first apply Ana has not accepted, so `accepted_user_id` is null and Terraform stops with an error on the member. Apply again after she accepts. To apply before everyone has accepted, add the member resources later, or look people up by email with the `heytownhall_members` data source and create members only for the addresses found:

```terraform
data "heytownhall_members" "all" {}

locals {
  user_id_by_email = { for m in data.heytownhall_members.all.members : m.email => m.user_id }
}

resource "heytownhall_member" "ana" {
  user_id = local.user_id_by_email["ana@example.com"]
  role    = "member"
}
```

Create members only for the people already in the map:

```terraform
variable "people" {
  type = set(string)
}

resource "heytownhall_invitation" "people" {
  for_each = var.people
  email    = each.key
}

resource "heytownhall_member" "people" {
  for_each = { for e in var.people : e => local.user_id_by_email[e] if contains(keys(local.user_id_by_email), e) }

  user_id = each.value
  role    = "member"
}
```

Run `terraform apply` again after people accept, and their member resources appear. Removing a `heytownhall_member` from the configuration removes the person from the organisation.

## Owners

A management token can never create, promote, demote or remove an owner. Owners are managed in the dashboard. Pointing `heytownhall_member` at an owner fails with the API's `owner_protected` error.
