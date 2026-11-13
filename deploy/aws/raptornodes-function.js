// CloudFront Function for raptornodes.net (runtime cloudfront-js-2.0, viewer
// request). The zone only holds node hostnames, <short id>.raptornodes.net,
// which point straight at nodes; this answers what's left, through the apex
// and wildcard records aimed at the distribution:
//   raptornodes.net       200: what the domain is, and where to report abuse
//   www.raptornodes.net   301 to https://raptornodes.net
//   any other name        404: no Raptor node there
// A node's own record always wins over the wildcard, so a real node's name
// never reaches this. Paste the whole file into the function, then publish.
var HEAD = '<!doctype html>\n<html lang="en">\n<head>\n<meta charset="utf-8">\n<meta name="viewport" content="width=device-width, initial-scale=1">\n<title>{{title}}</title>\n<link rel="icon" href="data:image/svg+xml,%3Csvg xmlns=%27http://www.w3.org/2000/svg%27 viewBox=%270 0 32 32%27%3E%3Crect width=%2732%27 height=%2732%27 rx=%276%27 fill=%27oklch(0.66 0.19 25)%27/%3E%3Cpath d=%27M81.8 0V-687.4H605.6Q681.1 -687.4 730.8 -657.2Q780.5 -626.9 805.3 -576.9Q830.2 -526.8 830.2 -466.8Q830.2 -401.2 799.4 -348.2Q768.5 -295.3 708.3 -264.9C845 -205 925 -95 935 40C880 -50 780 -150 600 -195L512.7 -232.4H278.7V0Z M278.7 -378H548.1Q584.8 -378 606.8 -400.7Q628.8 -423.4 628.8 -461.3Q628.8 -486.5 619 -504.2Q609.1 -521.9 591.1 -531.3Q573.1 -540.7 548.1 -540.7H278.7Z%27 transform=%27translate(1.76 25.6) scale(0.028)%27 fill-rule=%27evenodd%27 fill=%27oklch(0.17 0.03 25)%27/%3E%3C/svg%3E">\n<meta name="description" content="raptornodes.net names machines that people run game servers on with Raptor.">\n<style>\n:root{color-scheme:dark light;--bg:oklch(0.145 0.012 25);--raised:oklch(0.185 0.013 25);--rule:oklch(0.3 0.012 25);--text:oklch(0.96 0.005 25);--muted:oklch(0.72 0.011 25);--red:oklch(0.66 0.19 25);--sans:"Hanken Grotesk Variable","Hanken Grotesk",system-ui,sans-serif;--mono:"JetBrains Mono Variable","JetBrains Mono",ui-monospace,monospace}\n@media (prefers-color-scheme:light){:root{--bg:oklch(0.975 0.004 25);--raised:oklch(0.955 0.006 25);--rule:oklch(0.88 0.01 25);--text:oklch(0.18 0.018 25);--muted:oklch(0.44 0.013 25);--red:oklch(0.545 0.215 25)}}\n*{box-sizing:border-box}\nbody{margin:0;min-height:100vh;display:grid;place-items:center;padding:clamp(1.25rem,4vw,3rem);background:var(--bg);color:var(--text);font:400 1.0625rem/1.6 var(--sans)}\nmain{width:100%;max-width:36rem}\n.mark{display:block;width:9rem;margin:0 0 1.75rem}\n.mark svg{display:block;width:100%;height:auto}\n.mark .r{fill:var(--red)}\n.mark .w{fill:var(--text)}\nh1{font-size:clamp(1.75rem,5vw,2.25rem);line-height:1.15;letter-spacing:-.02em;margin:0 0 1rem;font-weight:650;text-wrap:balance}\nh2{font-size:1rem;line-height:1.3;margin:2rem 0 .375rem;font-weight:650}\np{margin:0 0 1rem;color:var(--muted)}\ncode,.term{font-family:var(--mono)}\ncode{font-size:.9em;color:var(--text)}\n.term{font-size:.875rem;line-height:1.5;background:var(--raised);border:1px solid var(--rule);border-radius:4px;padding:.875rem 1rem;margin:1.5rem 0;overflow-wrap:anywhere;color:var(--text)}\n.term span{color:var(--muted)}\na{color:var(--text);text-decoration-color:var(--red);text-underline-offset:.2em}\na:hover{color:var(--red)}\n</style>\n</head>\n<body>\n<main>\n<a class="mark" href="https://raptorpanel.net"><svg viewBox="0 0 289 46" role="img" aria-label="Raptor"><path class="r" transform="translate(0 -3)" fill-rule="evenodd" d="M2 14.5 C14 14.2 28 13.6 40 12.4 C47 11.8 55 12.6 60 14.4 C64 13.2 66.5 7.5 71 6 C75 4.6 79 4.4 83 5.6 C87 6.6 91 7.8 93.5 9 C94.3 9.4 94 10.6 93 10.6 L81.5 11.8 L89.5 13 C90.3 13.1 90.2 14.3 89.4 14.4 C85 15.2 79 15.4 75.5 16.2 C71.5 17.5 68.4 21.5 67.4 25.6 L72 29.4 L75.6 33.4 L73.8 33.2 L71 30.8 L73.2 33.8 L71.6 33.6 L69.4 31.2 L63.4 29.2 C60.5 29 57.5 28.4 55.4 28.2 C58.5 30.5 61.6 33.4 61.2 36.6 L57.2 42.4 L63.6 46.2 C64.4 46.8 64.2 48 63.2 48 L55.8 48 C54.4 47.6 53.2 46.4 52.6 44.8 C53.6 45.6 54.4 46 55.2 46.1 L53.2 42.8 L56.6 37.4 C56.6 34.4 53 31.8 50.4 31 C49 33.5 47.6 37.6 46 40.6 L39 44.4 L31.8 47.9 C30.8 48 30.3 47.1 31 46.6 L36.8 42.2 L42.4 38 C43.2 34 43.2 29 41 25.2 C38.4 21.4 34 19.6 28 18.2 C19 16.6 10 15.9 2 15.6 C1.3 15.5 1.3 14.6 2 14.5 Z M76.2 8 L80.8 7.2 C80.6 8.4 79.8 9.2 78.8 9.4 Z"/><path class="w" transform="translate(113.05 34.11) scale(0.03235)" d="M81.8 0V-687.4H605.6Q681.1 -687.4 730.8 -657.2Q780.5 -626.9 805.3 -576.9Q830.2 -526.8 830.2 -466.8Q830.2 -401.2 799.4 -348.2Q768.5 -295.3 708.3 -264.9L848.2 0H626.9L512.7 -232.4H278.7V0ZM278.7 -378H548.1Q584.8 -378 606.8 -400.7Q628.8 -423.4 628.8 -461.3Q628.8 -486.5 619 -504.2Q609.1 -521.9 591.1 -531.3Q573.1 -540.7 548.1 -540.7H278.7ZM954.2 0 1269.9 -687.4H1492.9L1808.7 0H1596.5L1547.7 -112.7H1205.2L1156.8 0ZM1266.5 -257.6H1486.4L1427.5 -398.7Q1423.5 -408.6 1417 -425.5Q1410.4 -442.4 1403.4 -461.7Q1396.5 -481 1390.3 -498.3Q1384.2 -515.6 1380.6 -525.5H1373.4Q1366.7 -507 1357.7 -483Q1348.7 -459 1340.3 -436.3Q1331.8 -413.5 1325.4 -398.4ZM1963.8 0V-687.4H2451.6Q2519.9 -687.4 2570.3 -658Q2620.8 -628.6 2648.7 -576.5Q2676.5 -524.4 2676.5 -455.7Q2676.5 -386.4 2648.1 -333.6Q2619.7 -280.8 2568.6 -251.6Q2517.6 -222.4 2449.4 -222.4H2160.7V0ZM2160.7 -369.7H2396.8Q2441.1 -369.7 2464.1 -391.9Q2487.1 -414 2487.1 -454.8Q2487.1 -482.4 2476.8 -501.2Q2466.5 -519.9 2446.5 -529.9Q2426.6 -539.8 2396.8 -539.8H2160.7ZM3043.1 0V-528.3H2766.2V-687.4H3516.9V-528.3H3240V0ZM4051.5 12Q3920.5 12 3824.8 -29.5Q3729.2 -71 3677.7 -150.4Q3626.2 -229.8 3626.2 -343.5Q3626.2 -458.2 3677.7 -537.5Q3729.2 -616.9 3824.8 -658.1Q3920.5 -699.4 4051.5 -699.4Q4184.2 -699.4 4279.3 -658.1Q4374.5 -616.9 4426 -537.5Q4477.5 -458.2 4477.5 -343.5Q4477.5 -229.8 4426 -150.4Q4374.5 -71 4279.3 -29.5Q4184.2 12 4051.5 12ZM4051.8 -135.8Q4102.7 -135.8 4143.9 -149Q4185.1 -162.1 4214.6 -187.2Q4244.1 -212.2 4259.8 -248.3Q4275.5 -284.4 4275.5 -329.5V-359.1Q4275.5 -404.1 4259.8 -440Q4244.1 -475.9 4214.6 -500.8Q4185.1 -525.6 4143.9 -538.6Q4102.7 -551.6 4051.8 -551.6Q4000.9 -551.6 3959.7 -538.6Q3918.5 -525.6 3889.1 -500.8Q3859.7 -475.9 3844.2 -440Q3828.7 -404.1 3828.7 -359.1V-329.5Q3828.7 -284.4 3844.2 -248.3Q3859.7 -212.2 3889.1 -187.2Q3918.5 -162.1 3959.7 -149Q4000.9 -135.8 4051.8 -135.8ZM4671.8 0V-687.4H5195.6Q5271.1 -687.4 5320.8 -657.2Q5370.5 -626.9 5395.3 -576.9Q5420.2 -526.8 5420.2 -466.8Q5420.2 -401.2 5389.4 -348.2Q5358.5 -295.3 5298.3 -264.9L5438.2 0H5216.9L5102.7 -232.4H4868.7V0ZM4868.7 -378H5138.1Q5174.8 -378 5196.8 -400.7Q5218.8 -423.4 5218.8 -461.3Q5218.8 -486.5 5209 -504.2Q5199.1 -521.9 5181.1 -531.3Q5163.1 -540.7 5138.1 -540.7H4868.7Z"/></svg></a>\n';
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
      'content-security-policy': { value: "default-src 'none'; img-src data:; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'" },
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
