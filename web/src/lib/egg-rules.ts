// Variable rules for the egg editor: authors pick a kind, and the egg keeps
// the Laravel rules Wings checks. Kept free of imports so `node --test` can
// check it.

export type VariableKind = "text" | "number" | "toggle" | "choice" | "custom";

export const kindNames: Record<VariableKind, string> = {
  text: "Text",
  number: "Number",
  toggle: "On / off",
  choice: "Choice",
  custom: "Custom rules",
};

export type VariableForm = {
  kind: VariableKind;
  required: boolean;
  max: string; // text: longest; number: highest
  min: string; // number: lowest
  options: string[]; // choice
};

const toggleSets = ["0,1", "1,0", "true,false", "false,true"];

// readRules turns rules into the form, when they're ones the kinds make.
export function readRules(rules: string[]): VariableForm {
  const form: VariableForm = { kind: "text", required: false, max: "", min: "", options: [] };
  let kind: VariableKind | undefined;
  for (const r of rules) {
    const [name, arg = ""] = r.split(/:(.*)/s);
    switch (name) {
      case "required":
        form.required = true;
        break;
      case "nullable":
      case "sometimes":
        break;
      case "string":
        kind ??= "text";
        break;
      case "integer":
      case "numeric":
        kind = "number";
        break;
      case "boolean":
        kind = "toggle";
        break;
      case "in":
        if (toggleSets.includes(arg.replaceAll(" ", ""))) kind = "toggle";
        else {
          kind = "choice";
          form.options = arg.split(",");
        }
        break;
      case "max":
        form.max = arg;
        break;
      case "min":
        form.min = arg;
        break;
      case "between": {
        const [lo = "", hi = ""] = arg.split(",");
        form.min = lo;
        form.max = hi;
        break;
      }
      default:
        return { ...form, kind: "custom" };
    }
  }
  form.kind = kind ?? "text";
  // Rules the form wouldn't write back the same way stay custom.
  if (writeRules(form).join("|") !== normalize(rules).join("|")) form.kind = "custom";
  return form;
}

function normalize(rules: string[]): string[] {
  return rules.filter((r) => r !== "sometimes");
}

// writeRules is the rules for a form (not for custom, whose rules are its
// own).
export function writeRules(f: VariableForm): string[] {
  const out = [f.required ? "required" : "nullable"];
  switch (f.kind) {
    case "text":
      out.push("string");
      if (f.max) out.push(`max:${f.max}`);
      break;
    case "number":
      out.push("integer");
      if (f.min) out.push(`min:${f.min}`);
      if (f.max) out.push(`max:${f.max}`);
      break;
    case "toggle":
      out.push("boolean");
      break;
    case "choice":
      out.push(
        `in:${f.options
          .map((o) => o.trim())
          .filter(Boolean)
          .join(",")}`,
      );
      break;
  }
  return out;
}
