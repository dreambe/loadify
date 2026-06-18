"use client";

// TableSkeleton renders shimmering placeholder rows while a list loads, so the
// page has stable structure instead of flashing empty then filling in.
export default function TableSkeleton({ cols, rows = 5 }: { cols: number; rows?: number }) {
  return (
    <table>
      <tbody>
        {Array.from({ length: rows }).map((_, r) => (
          <tr key={r}>
            {Array.from({ length: cols }).map((_, c) => (
              <td key={c}>
                <div
                  className="skeleton"
                  style={{ height: 14, width: c === 0 ? "70%" : `${40 + ((c * 17) % 45)}%` }}
                />
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}
