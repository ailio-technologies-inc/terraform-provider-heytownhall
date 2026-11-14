# aws_route53_record.sso_verify (or a Cloudflare record) publishes heytownhall_sso_domain.acme.verification_record.
resource "heytownhall_sso_domain_verification" "acme" {
  sso_domain_id = heytownhall_sso_domain.acme.id

  timeouts = {
    create = "15m"
  }

  depends_on = [aws_route53_record.sso_verify]
}
