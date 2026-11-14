resource "heytownhall_invitation" "ana" {
  email = "ana@example.com"
  role  = "member"
}

# Once Ana accepts, the invitation stays in the state with status "accepted" and Terraform plans nothing.
# accepted_user_id then names her, so her role can be managed:
resource "heytownhall_member" "ana" {
  user_id = heytownhall_invitation.ana.accepted_user_id
  role    = "member"
}
