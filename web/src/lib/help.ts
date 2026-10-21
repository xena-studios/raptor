// Where owners get help (docs/DECISIONS.md #292): the docs site, and
// the Discord server for questions and tickets. The app only links to
// them; nothing of theirs runs here.

export const docsUrl = "https://docs.raptorpanel.net";
export const discordUrl = "https://discord.gg/HyUa3sRpeN";

// docs is a page of the docs, by its path ("/help/install-problems/").
export function docs(path: string): string {
  return docsUrl + path;
}
