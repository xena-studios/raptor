// CloudFront Function for raptornodes.net (runtime cloudfront-js-2.0, viewer
// request). The zone only holds node hostnames, <short id>.raptornodes.net,
// which point straight at nodes; this answers what's left, through the apex
// and wildcard records aimed at the distribution:
//   raptornodes.net       200: what the domain is, and where to report abuse
//   www.raptornodes.net   301 to https://raptornodes.net
//   any other name        404: no Raptor node there
// A node's own record always wins over the wildcard, so a real node's name
// never reaches this. Paste the whole file into the function, then publish.
var HEAD = '<!doctype html>\n<html lang="en">\n<head>\n<meta charset="utf-8">\n<meta name="viewport" content="width=device-width, initial-scale=1">\n<title>{{title}}</title>\n<meta name="description" content="raptornodes.net names machines that people run game servers on with Raptor.">\n<style>\n:root{color-scheme:dark light;--bg:oklch(0.145 0.012 25);--raised:oklch(0.185 0.013 25);--rule:oklch(0.3 0.012 25);--text:oklch(0.96 0.005 25);--muted:oklch(0.72 0.011 25);--red:oklch(0.66 0.19 25);--sans:"Hanken Grotesk Variable","Hanken Grotesk",system-ui,sans-serif;--mono:"JetBrains Mono Variable","JetBrains Mono",ui-monospace,monospace}\n@media (prefers-color-scheme:light){:root{--bg:oklch(0.975 0.004 25);--raised:oklch(0.955 0.006 25);--rule:oklch(0.88 0.01 25);--text:oklch(0.18 0.018 25);--muted:oklch(0.44 0.013 25);--red:oklch(0.545 0.215 25)}}\n*{box-sizing:border-box}\nbody{margin:0;min-height:100vh;display:grid;place-items:center;padding:clamp(1.25rem,4vw,3rem);background:var(--bg);color:var(--text);font:400 1.0625rem/1.6 var(--sans)}\nmain{width:100%;max-width:36rem}\n.mark{font:600 .8125rem/1 var(--mono);letter-spacing:.08em;text-transform:uppercase;color:var(--red);margin:0 0 1.5rem}\nh1{font-size:clamp(1.75rem,5vw,2.25rem);line-height:1.15;letter-spacing:-.02em;margin:0 0 1rem;font-weight:650;text-wrap:balance}\nh2{font-size:1rem;line-height:1.3;margin:2rem 0 .375rem;font-weight:650}\np{margin:0 0 1rem;color:var(--muted)}\ncode,.term{font-family:var(--mono)}\ncode{font-size:.9em;color:var(--text)}\n.term{font-size:.875rem;line-height:1.5;background:var(--raised);border:1px solid var(--rule);border-radius:4px;padding:.875rem 1rem;margin:1.5rem 0;overflow-wrap:anywhere;color:var(--text)}\n.term span{color:var(--muted)}\na{color:var(--text);text-decoration-color:var(--red);text-underline-offset:.2em}\na:hover{color:var(--red)}\n</style>\n</head>\n<body>\n<main>\n<p class="mark">Raptor</p>\n';
var FOOT = '</main>\n</body>\n</html>\n';

var ABOUT = '<h1>raptornodes.net names machines that run game servers.</h1>\n' +
  '<p>Every name under it, like <code>3f9a1c2b7e4d.raptornodes.net</code>, points at a machine someone runs their game servers on with <a href="https://raptorpanel.net">Raptor</a>. Each one belongs to its owner, not to Raptor: Raptor doesn\'t host the games, their files, or their traffic, and there\'s no website at a node\'s name.</p>\n' +
  '<h2>Playing on a server</h2>\n<p>Connect with the address its owner gave you. If it doesn\'t work, ask them: only they can see their machine.</p>\n' +
  '<h2>Reporting abuse</h2>\n<p>A raptornodes.net name used for phishing, malware, spam, or attacks? Email <a href="mailto:abuse@raptorpanel.net">abuse@raptorpanel.net</a> with the name and what you saw. Raptor can stop a name pointing at a machine; the machine itself is its owner\'s.</p>\n' +
  '<h2>Running game servers</h2>\n<p>That\'s what <a href="https://raptorpanel.net">Raptor</a> is for.</p>\n';

var MISSING = '<h1>There\'s no Raptor node at this address.</h1>\n' +
  '<div class="term"><span>$</span> resolve {{name}}<br><span>no node with this name</span></div>\n' +
  '<p>Every Raptor node gets its own name here, for players to connect to. This one doesn\'t belong to a node: it may be misspelled, or the node it named was removed.</p>\n' +
  '<p>Playing on a server? Check the address with whoever runs it. Running one? Your node\'s address is on its page in the <a href="https://app.raptorpanel.net">Raptor app</a>.</p>\n' +
  '<p><a href="https://raptornodes.net">What raptornodes.net is</a></p>\n';

function page(status, description, title, body, robots) {
  return {
    statusCode: status,
    statusDescription: description,
    headers: {
      'content-type': { value: 'text/html; charset=utf-8' },
      'cache-control': { value: 'public, max-age=300' },
      'content-security-policy': { value: "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'" },
      'x-content-type-options': { value: 'nosniff' },
      'referrer-policy': { value: 'no-referrer' },
      'x-robots-tag': { value: robots }
    },
    body: { encoding: 'text', data: HEAD.split('{{title}}').join(title) + body + FOOT }
  };
}

function handler(event) {
  var h = event.request.headers.host;
  var host = h ? h.value.toLowerCase() : '';
  if (host === 'raptornodes.net') {
    return page(200, 'OK', 'raptornodes.net · Raptor', ABOUT, 'all');
  }
  if (host === 'www.raptornodes.net') {
    return {
      statusCode: 301,
      statusDescription: 'Moved Permanently',
      headers: { location: { value: 'https://raptornodes.net' } }
    };
  }
  // Only what a hostname can hold goes into the page.
  var name = host.replace(/[^a-z0-9.-]/g, '').slice(0, 253) || 'this name';
  return page(404, 'Not Found', 'No Raptor node here', MISSING.split('{{name}}').join(name), 'noindex');
}
