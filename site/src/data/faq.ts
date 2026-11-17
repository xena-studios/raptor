// Questions people ask first: the home page shows `general`, the pricing
// page `billing`.
import { price } from "./links";

export const general: [string, string][] = [
  ["What do I need?", "A Linux machine you can run commands on as root: a dedicated server, a VPS, or a box at home, with Debian 12 or 13 or Ubuntu 24.04, on x86 or ARM. Raptor installs Docker and everything else."],
  ["Do I have to open ports?", "Not for Raptor: your machine connects out to it. Players need the game's own port open, and the connection test tells you if they can't reach it, with steps for your provider's firewall."],
  ["What happens if Raptor goes down?", "Nothing your players notice. Servers keep running, schedules keep firing, and backups keep happening on the machine, and the raptor command there still starts, stops, and shows logs."],
  ["My game isn't in the library.", "Raptor reads Pterodactyl eggs, so anything with one can run: import it from a link or a file in the app. Or ask on Discord, and we'll add it to the library."],
  ["I'm on Pterodactyl. Can I move?", "Yes, without starting over. Raptor runs the same eggs, and raptor import pterodactyl moves your servers over on the same machine, files and all, while Pterodactyl's copies stay untouched until you remove them."],
  ["Is it open source?", "All of it, under the AGPL-3.0: the panel, the web app, and everything that runs on your machine."],
];

export const billing: [string, string][] = [
  ["Is the first month really free?", `Yes. Link your first machine and Raptor costs nothing for a month. A card is needed to start; you're charged $${price} when the month is up, unless you cancel first, in the app's billing settings.`],
  ["What counts as a machine?", "Each node you link: a dedicated server, a VPS, or a box at home. Everything on it is included, however many servers, people, and backups."],
  ["Are there limits on servers, RAM, or players?", "No. It's your hardware, so you run as much as it can handle. Ten servers on one machine is still one price."],
  ["What does backup storage cost?", `Backups on your machine, or in your own S3, B2, or SFTP storage, are free. Raptor Backup Storage includes 10 GB per machine, then $${price} per TB a month.`],
  ["What if I stop paying?", "The panel goes read-only for that machine. Raptor never stops your servers over billing, and the raptor command on the machine keeps working."],
  ["Pterodactyl is free. Why pay?", `Pterodactyl's code is free; running it isn't. You host and patch the panel, buy add-ons for what Raptor has built in, and lose control of your servers whenever the panel is down. If you'd rather just run games, that's what the $${price} is for.`],
  ["Who handles taxes and invoices?", "Polar bills for us, as the merchant of record: it works out sales tax and VAT where you are, and sends the invoices."],
];
