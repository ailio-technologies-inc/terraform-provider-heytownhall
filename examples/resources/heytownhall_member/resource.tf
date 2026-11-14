# The user id of a person who has already accepted an invitation.
data "heytownhall_members" "all" {}

locals {
  user_id_by_email = { for m in data.heytownhall_members.all.members : m.email => m.user_id }
}

resource "heytownhall_member" "ana" {
  user_id = local.user_id_by_email["ana@example.com"]
  role    = "member"
}
