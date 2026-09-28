import { useEffect, useState, type DependencyList } from "react";
import { errorMessage, isAbort } from "./api";

export interface AsyncState<T> {
  data: T | undefined;
  error: string | null;
  loading: boolean;
}

/**
 * Runs `fn` whenever `deps` change, aborting the previous request.
 * Keeps the previous data while reloading so panels don't flash.
 * Pass `enabled = false` to skip (data resets to undefined).
 */
export function useAsync<T>(
  fn: (signal: AbortSignal) => Promise<T>,
  deps: DependencyList,
  enabled = true,
): AsyncState<T> {
  const [state, setState] = useState<AsyncState<T>>({ data: undefined, error: null, loading: enabled });

  useEffect(() => {
    if (!enabled) {
      setState({ data: undefined, error: null, loading: false });
      return;
    }
    const ctrl = new AbortController();
    setState((s) => ({ ...s, loading: true, error: null }));
    fn(ctrl.signal).then(
      (data) => {
        if (!ctrl.signal.aborted) setState({ data, error: null, loading: false });
      },
      (e: unknown) => {
        if (ctrl.signal.aborted || isAbort(e)) return;
        setState((s) => ({ data: s.data, error: errorMessage(e), loading: false }));
      },
    );
    return () => ctrl.abort();
  }, [...deps, enabled]);

  return state;
}
