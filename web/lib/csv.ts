// parseCSV turns CSV text into an array of objects keyed by the header row.
// Minimal RFC-4180-ish parser: handles quoted fields, embedded commas/newlines
// and doubled quotes. No external dependency.
export function parseCSV(text: string): Record<string, string>[] {
  const rows = parseRows(text);
  if (rows.length === 0) return [];
  const headers = rows[0];
  const out: Record<string, string>[] = [];
  for (let i = 1; i < rows.length; i++) {
    const r = rows[i];
    if (r.length === 1 && r[0] === "") continue; // skip blank lines
    const obj: Record<string, string> = {};
    headers.forEach((h, j) => {
      obj[h.trim()] = (r[j] ?? "").trim();
    });
    out.push(obj);
  }
  return out;
}

function parseRows(text: string): string[][] {
  const rows: string[][] = [];
  let field = "";
  let row: string[] = [];
  let inQuotes = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (inQuotes) {
      if (c === '"') {
        if (text[i + 1] === '"') {
          field += '"';
          i++;
        } else {
          inQuotes = false;
        }
      } else {
        field += c;
      }
      continue;
    }
    switch (c) {
      case '"':
        inQuotes = true;
        break;
      case ",":
        row.push(field);
        field = "";
        break;
      case "\r":
        break;
      case "\n":
        row.push(field);
        rows.push(row);
        row = [];
        field = "";
        break;
      default:
        field += c;
    }
  }
  if (field !== "" || row.length > 0) {
    row.push(field);
    rows.push(row);
  }
  return rows;
}
