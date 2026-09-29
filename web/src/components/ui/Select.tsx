import * as RSelect from "@radix-ui/react-select";

// shadcn/ui's Select, on the same Radix primitive, styled by styles.css (.sel-*).
// Radix reserves "" for "no value", so an empty option value travels as NONE.

const NONE = "__none__";

export interface SelectOption {
  value: string;
  label: string;
}

interface Props {
  value: string;
  options: SelectOption[];
  onChange: (value: string) => void;
  label: string;
  disabled?: boolean;
  id?: string;
}

export function Select({ value, options, onChange, label, disabled, id }: Props) {
  return (
    <RSelect.Root value={value || NONE} onValueChange={(v) => onChange(v === NONE ? "" : v)} disabled={disabled}>
      <RSelect.Trigger className="sel-trigger" id={id} aria-label={label}>
        <RSelect.Value />
        <RSelect.Icon className="sel-chevron">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M6 9l6 6 6-6" />
          </svg>
        </RSelect.Icon>
      </RSelect.Trigger>
      <RSelect.Portal>
        <RSelect.Content className="sel-content" position="popper" sideOffset={6}>
          <RSelect.Viewport className="sel-viewport">
            {options.map((o) => (
              <RSelect.Item key={o.value || NONE} value={o.value || NONE} className="sel-item">
                <RSelect.ItemText>{o.label}</RSelect.ItemText>
                <RSelect.ItemIndicator className="sel-check">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M5 12.5l4.5 4.5L19 7" />
                  </svg>
                </RSelect.ItemIndicator>
              </RSelect.Item>
            ))}
          </RSelect.Viewport>
        </RSelect.Content>
      </RSelect.Portal>
    </RSelect.Root>
  );
}
