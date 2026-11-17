package demo

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"time"
)

// AllowedHosts are what a running sandbox may reach (on web ports), as the
// demo's egress policy has it (firewall.Demo): what game servers fetch
// while they run. Anything else from a sandbox is dropped.
var AllowedHosts = []string{
	// Modrinth.
	"api.modrinth.com", "cdn.modrinth.com",
	// Hangar (PaperMC's plugins).
	"hangar.papermc.io", "hangarcdn.papermc.io",
	// PaperMC's builds and libraries.
	"api.papermc.io", "fill.papermc.io", "fill-data.papermc.io", "repo.papermc.io",
	// uMod (Rust's and others' plugins).
	"umod.org", "assets.umod.org",
	// Mojang: sign-in, skins, and the game's own downloads.
	"sessionserver.mojang.com", "api.mojang.com", "api.minecraftservices.com",
	"launchermeta.mojang.com", "piston-meta.mojang.com", "piston-data.mojang.com",
	"libraries.minecraft.net", "resources.download.minecraft.net", "textures.minecraft.net",
	// Steam's web API and content (its game servers' own ports are
	// allowed separately).
	"api.steampowered.com", "steamcommunity.com", "steamcdn-a.akamaihd.net", "cdn.cloudflare.steamstatic.com",
	// CurseForge's CDN.
	"edge.forgecdn.net", "mediafilez.forgecdn.net", "api.curseforge.com",
	// Thunderstore's.
	"thunderstore.io", "gcdn.thunderstore.io", "ccdn.thunderstore.io",
}

// Resolver looks names up (net.DefaultResolver).
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// ResolveAllowed looks up AllowedHosts' addresses. A name that doesn't
// resolve is left out (and logged); the rest still are.
func ResolveAllowed(ctx context.Context, r Resolver, log *slog.Logger) []netip.Addr {
	if r == nil {
		r = net.DefaultResolver
	}
	var out []netip.Addr
	for _, h := range AllowedHosts {
		lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		addrs, err := r.LookupNetIP(lctx, "ip", h)
		cancel()
		if err != nil {
			log.Warn("demo: an allowed host doesn't resolve; sandboxes can't reach it until it does", "host", h, "err", err)
			continue
		}
		for _, a := range addrs {
			out = append(out, a.Unmap().WithZone(""))
		}
	}
	slices.SortFunc(out, netip.Addr.Compare)
	return slices.Compact(out)
}

// EgressEvery is how often the allowed hosts are looked up again: CDNs'
// addresses move.
const EgressEvery = 10 * time.Minute

// RunEgress looks the allowed hosts up now and every EgressEvery, and
// hands the addresses to apply (which reloads the firewall), until ctx
// ends.
func RunEgress(ctx context.Context, r Resolver, log *slog.Logger, apply func(ctx context.Context, allow []netip.Addr) error) {
	t := time.NewTicker(EgressEvery)
	defer t.Stop()
	var last []netip.Addr
	for {
		allow := ResolveAllowed(ctx, r, log)
		if !slices.Equal(allow, last) {
			if err := apply(ctx, allow); err != nil {
				log.Error("demo: loading the sandboxes' allowed hosts into the firewall failed", "err", err)
			} else {
				last = allow
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
