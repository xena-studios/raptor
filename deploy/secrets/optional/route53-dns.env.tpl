# Nodes' hostnames in raptornodes.net, in its Route 53 hosted zone
# (docs/DEPLOY.md#raptornodesnet). Optional: skipped until the item
# "Route 53 DNS" exists in the vault (scripts/secrets.sh). It wins over the
# Cloudflare item when both are there.
PANEL_ROUTE53_ACCESS_KEY_ID={{ op://Raptor production/Route 53 DNS/access_key_id }}
PANEL_ROUTE53_SECRET_ACCESS_KEY={{ op://Raptor production/Route 53 DNS/secret_access_key }}
PANEL_ROUTE53_ZONE_ID={{ op://Raptor production/Route 53 DNS/zone_id }}
