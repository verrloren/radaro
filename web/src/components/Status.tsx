export function ErrorLine({ error, onRetry }: { error: string | null | undefined; onRetry?: () => void }) {
  if (!error) return null;
  return (
    <p className="error-line" role="alert">
      <span aria-hidden="true">!</span> {error}
      {onRetry && (
        <button type="button" className="link-btn" onClick={onRetry}>
          retry
        </button>
      )}
    </p>
  );
}

export function Loading({ label = "Loading" }: { label?: string }) {
  return (
    <p className="loading-line" role="status">
      <span className="pulse" aria-hidden="true" /> {label}…
    </p>
  );
}
