export function EmptyState({ onScan }: { onScan: () => void }) {
  return (
    <section className="card pad empty" aria-labelledby="empty-h">
      <span className="tri" aria-hidden="true">
        <i />
        <i />
        <i />
      </span>
      <h2 id="empty-h" className="empty-title">
        nothing heard yet — scan it
      </h2>
      <p className="muted">Radaro hasn't tracked any keywords. Start with one of these:</p>
      <ul className="empty-steps">
        <li>
          <span className="step">
            <b>1</b>
          </span>
          <span>
            Open{" "}
            <button type="button" className="link-btn" onClick={onScan}>
              Scan
            </button>
            , type a keyword and press “Scan latest”.
          </span>
        </li>
        <li>
          <span className="step">
            <b>2</b>
          </span>
          <span>
            Or run <code>radaro track "keyword"</code> in a terminal.
          </span>
        </li>
        <li>
          <span className="step">
            <b>3</b>
          </span>
          <span>
            Just exploring? <code>radaro demo</code> loads a sample dataset offline.
          </span>
        </li>
      </ul>
    </section>
  );
}
