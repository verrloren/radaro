export function EmptyState() {
  return (
    <section className="panel empty" aria-labelledby="empty-h">
      <svg className="empty-mark" viewBox="0 0 64 64" aria-hidden="true">
        <circle cx="32" cy="32" r="28" />
        <circle cx="32" cy="32" r="18" />
        <circle cx="32" cy="32" r="8" />
        <path d="M32 32 L54 18" />
      </svg>
      <h2 id="empty-h" className="empty-title">
        Nothing on the radar yet
      </h2>
      <p>Radaro hasn't tracked any keywords. Start with one of these:</p>
      <ul className="empty-steps">
        <li>
          <code>radaro demo</code>
          <span>loads a sample dataset so you can explore the dashboard offline.</span>
        </li>
        <li>
          <code>radaro track "keyword"</code>
          <span>fetches real mentions from your configured sources.</span>
        </li>
        <li>
          <span className="mono">or</span>
          <span>type a keyword in the Scan panel and press “Scan latest”.</span>
        </li>
      </ul>
    </section>
  );
}
