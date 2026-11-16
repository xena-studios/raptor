import assert from "node:assert/strict";
import { test } from "node:test";

import { domainName, domainRecords, serverAddress, subdomainLabel } from "./addresses.ts";

test("server addresses", () => {
  const n = { shortId: "1a2b3c4d5e6f" };
  // Without a shown address: the node's hostname, with the port.
  assert.deepEqual(serverAddress(n, { displayAddress: "" }, 25565), {
    text: "1a2b3c4d5e6f.raptornodes.net:25565",
    private: true,
  });
  // A shown address is shown as typed: no port added.
  assert.deepEqual(serverAddress(n, { displayAddress: "play.example.com" }, 25565), {
    text: "play.example.com",
    private: false,
  });
  assert.equal(
    serverAddress(n, { displayAddress: "mc.example.com:25565" }, 30001).text,
    "mc.example.com:25565",
  );
});

test("domain names for your own domain", () => {
  assert.equal(domainName("play.example.com"), "play.example.com");
  // Cleaned up: case, scheme, path, port, trailing dot, spaces.
  assert.equal(domainName("  https://Play.Example.com:25565/x "), "play.example.com");
  assert.equal(domainName("play.example.com."), "play.example.com");
  assert.equal(domainName("mc.example.co.uk"), "mc.example.co.uk");
  // Not domain names.
  for (const bad of [
    "",
    "localhost",
    "1.2.3.4",
    "play..example.com",
    "-a.example.com",
    "a_b.example.com",
  ]) {
    assert.equal(domainName(bad), "", bad);
  }
});

test("records for your own domain", () => {
  const base = {
    domain: "example.com",
    host: "1a2b3c4d5e6f.raptornodes.net",
    ipv4: "203.0.113.7",
    port: 25601,
  };
  // A subdomain: a CNAME to the node, and for Minecraft Java an SRV too.
  let r = domainRecords({ ...base, sub: "play", srv: true });
  assert.equal(r.shown, "play.example.com");
  assert.deepEqual(
    r.records.map((x) => [x.type, x.name, x.full]),
    [
      ["CNAME", "play", "play.example.com"],
      ["SRV", "_minecraft._tcp.play", "_minecraft._tcp.play.example.com"],
    ],
  );
  // Without SRV, players type the port.
  r = domainRecords({ ...base, sub: "play", srv: false });
  assert.equal(r.shown, "play.example.com:25601");
  assert.equal(r.records.length, 1);
  // The domain itself, Minecraft Java: the SRV record alone.
  r = domainRecords({ ...base, sub: "", srv: true });
  assert.equal(r.shown, "example.com");
  assert.deepEqual(
    r.records.map((x) => [x.type, x.name]),
    [["SRV", "_minecraft._tcp"]],
  );
  // The domain itself, other games: an A record with the IPv4.
  r = domainRecords({ ...base, sub: "", srv: false });
  assert.equal(r.shown, "example.com:25601");
  assert.deepEqual(r.records[0], {
    type: "A",
    name: "@",
    full: "example.com",
    value: "203.0.113.7",
    private: true,
  });
});

test("subdomain labels", () => {
  assert.equal(subdomainLabel(" Play "), "play");
  assert.equal(subdomainLabel("mc.eu"), "mc.eu");
  assert.equal(subdomainLabel("bad_name"), "");
  assert.equal(subdomainLabel(""), "");
});
