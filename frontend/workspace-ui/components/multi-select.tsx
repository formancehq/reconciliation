'use client';

import { cn } from '@workspace/ui/lib/utils';
import { Badge } from '@workspace/ui/components/badge';
import { Button } from '@workspace/ui/components/button';
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@workspace/ui/components/command';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@workspace/ui/components/popover';
import { cva, type VariantProps } from 'class-variance-authority';
import { Command as CommandPrimitive } from 'cmdk';
import { CheckIcon, ChevronsUpDownIcon, PlusIcon, XIcon } from 'lucide-react';
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ComponentPropsWithoutRef,
  type KeyboardEvent,
  type ReactNode,
} from 'react';

type MultiSelectMode = 'popover' | 'inline';

type MultiSelectContextType = {
  mode: MultiSelectMode;
  open: boolean;
  setOpen: (open: boolean) => void;
  selectedValues: Set<string>;
  toggleValue: (value: string) => void;
  removeLastValue: () => void;
  items: Map<string, ReactNode>;
  onItemAdded: (value: string, label: ReactNode) => void;
  inputValue: string;
  setInputValue: (value: string) => void;
  disabled?: boolean;
};
const MultiSelectContext = createContext<MultiSelectContextType | null>(null);

export function MultiSelect({
  children,
  values,
  defaultValues,
  onValuesChange,
  disabled,
  mode = 'popover',
}: {
  children: ReactNode;
  values?: string[];
  defaultValues?: string[];
  onValuesChange?: (values: string[]) => void;
  disabled?: boolean;
  mode?: MultiSelectMode;
}) {
  const [open, setOpenState] = useState(false);
  const [internalValues, setInternalValues] = useState(
    new Set<string>(values ?? defaultValues)
  );
  const selectedValues = values ? new Set(values) : internalValues;
  const [items, setItems] = useState<Map<string, ReactNode>>(new Map());
  const [inputValue, setInputValue] = useState('');
  const containerRef = useRef<HTMLDivElement>(null);

  const setOpen = useCallback(
    (next: boolean) => {
      if (disabled) return;
      setOpenState(next);
      if (!next) setInputValue('');
    },
    [disabled]
  );

  function toggleValue(value: string) {
    const getNewSet = (prev: Set<string>) => {
      const newSet = new Set(prev);
      if (newSet.has(value)) {
        newSet.delete(value);
      } else {
        newSet.add(value);
      }

      return newSet;
    };
    setInternalValues(getNewSet);
    onValuesChange?.([...getNewSet(selectedValues)]);
  }

  function removeLastValue() {
    const vals = [...selectedValues];
    if (vals.length === 0) return;
    const last = vals[vals.length - 1]!;
    toggleValue(last);
  }

  const onItemAdded = useCallback((value: string, label: ReactNode) => {
    setItems((prev) => {
      if (prev.get(value) === label) return prev;

      return new Map(prev).set(value, label);
    });
  }, []);

  // Click outside for inline mode
  useEffect(() => {
    if (mode !== 'inline' || !open) return;
    function handlePointerDown(e: PointerEvent) {
      if (
        containerRef.current &&
        !containerRef.current.contains(e.target as Node)
      ) {
        setOpen(false);
      }
    }
    document.addEventListener('pointerdown', handlePointerDown);

    return () => document.removeEventListener('pointerdown', handlePointerDown);
  }, [mode, open, setOpen]);

  const contextValue: MultiSelectContextType = {
    mode,
    open,
    setOpen,
    selectedValues,
    toggleValue,
    removeLastValue,
    items,
    onItemAdded,
    inputValue,
    setInputValue,
    disabled,
  };

  if (mode === 'inline') {
    return (
      <MultiSelectContext value={contextValue}>
        <div ref={containerRef} className="relative">
          <Command
            className="overflow-visible bg-transparent"
            onKeyDown={(e) => {
              if (e.key === 'Escape') setOpen(false);
            }}
          >
            {children}
          </Command>
        </div>
      </MultiSelectContext>
    );
  }

  return (
    <MultiSelectContext value={contextValue}>
      <Popover
        open={disabled ? false : open}
        onOpenChange={setOpen}
        modal={true}
      >
        {children}
      </Popover>
    </MultiSelectContext>
  );
}

const multiSelectTriggerVariants = cva('', {
  variants: {
    size: {
      sm: 'min-h-7 py-1',
      md: 'min-h-8 py-1.5',
      lg: 'min-h-9 py-2',
    },
  },
  defaultVariants: {
    size: 'md',
  },
});

type MultiSelectTriggerProps = {
  className?: string;
  children?: ReactNode;
} & VariantProps<typeof multiSelectTriggerVariants> &
  ComponentPropsWithoutRef<typeof Button>;

export function MultiSelectTrigger({
  className,
  children,
  size = 'md',
  ...props
}: MultiSelectTriggerProps) {
  const { mode, open, setOpen, disabled } = useMultiSelectContext();

  if (mode === 'inline') {
    return (
      <div
        role="combobox"
        aria-expanded={open}
        aria-disabled={disabled}
        onClick={() => !disabled && setOpen(true)}
        className={cn(
          'flex h-auto w-fit items-center gap-1.5 rounded-md border border-input bg-background px-3 text-sm transition-[color] outline-none dark:bg-input/30 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*=size-])]:size-4 [&_svg:not([class*=text-])]:text-muted-foreground',
          multiSelectTriggerVariants({ size }),
          open && 'ring-[3px] ring-ring/50 border-ring',
          disabled && 'cursor-not-allowed opacity-50',
          className
        )}
      >
        {children}
      </div>
    );
  }

  return (
    <PopoverTrigger asChild>
      <Button
        {...props}
        variant={props.variant ?? 'outline'}
        role={props.role ?? 'combobox'}
        aria-expanded={props['aria-expanded'] ?? open}
        disabled={disabled ?? props.disabled}
        className={cn(
          "flex h-auto w-fit items-center justify-between gap-2 overflow-hidden rounded-md border border-input bg-background dark:bg-input/30 px-3 text-sm whitespace-nowrap transition-[color] outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-destructive/20 data-[placeholder]:text-muted-foreground dark:hover:bg-input/50 dark:aria-invalid:ring-destructive/40 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 [&_svg:not([class*='text-'])]:text-muted-foreground",
          multiSelectTriggerVariants({ size }),
          className
        )}
      >
        {children}
        <ChevronsUpDownIcon className="size-4 shrink-0 opacity-50" />
      </Button>
    </PopoverTrigger>
  );
}

export function MultiSelectInput({
  className,
  ...props
}: ComponentPropsWithoutRef<typeof CommandPrimitive.Input>) {
  const {
    inputValue,
    setInputValue,
    setOpen,
    removeLastValue,
    selectedValues,
  } = useMultiSelectContext();
  const inputRef = useRef<HTMLInputElement>(null);

  return (
    <CommandPrimitive.Input
      ref={inputRef}
      value={inputValue}
      onValueChange={(value) => {
        setInputValue(value);
        setOpen(true);
      }}
      onFocus={() => setOpen(true)}
      onKeyDown={(e) => {
        if (
          (e.key === 'Backspace' || e.key === 'Delete') &&
          inputValue === '' &&
          selectedValues.size > 0
        ) {
          e.preventDefault();
          removeLastValue();
        }
      }}
      className={cn(
        'flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground min-w-[80px]',
        className
      )}
      {...props}
    />
  );
}

export function MultiSelectValue({
  placeholder,
  clickToRemove = true,
  className,
  overflowBehavior = 'wrap-when-open',
  ...props
}: {
  placeholder?: string;
  clickToRemove?: boolean;
  overflowBehavior?: 'wrap' | 'wrap-when-open' | 'cutoff';
} & Omit<ComponentPropsWithoutRef<'div'>, 'children'>) {
  const { selectedValues, toggleValue, removeLastValue, items, open, mode } =
    useMultiSelectContext();
  const [overflowAmount, setOverflowAmount] = useState(0);
  const valueRef = useRef<HTMLDivElement>(null);
  const overflowRef = useRef<HTMLDivElement>(null);

  const shouldWrap =
    overflowBehavior === 'wrap' ||
    (overflowBehavior === 'wrap-when-open' && open);

  const checkOverflow = useCallback(() => {
    if (valueRef.current == null) return;

    const containerElement = valueRef.current;
    const overflowElement = overflowRef.current;
    const items = containerElement.querySelectorAll<HTMLElement>(
      '[data-selected-item]'
    );

    if (overflowElement != null) overflowElement.style.display = 'none';
    items.forEach((child) => child.style.removeProperty('display'));
    let amount = 0;
    for (let i = items.length - 1; i >= 0; i--) {
      const child = items[i]!;
      if (containerElement.scrollWidth <= containerElement.clientWidth) {
        break;
      }
      amount = items.length - i;
      child.style.display = 'none';
      overflowElement?.style.removeProperty('display');
    }
    setOverflowAmount(amount);
  }, []);

  const handleResize = useCallback(
    (node: HTMLDivElement) => {
      valueRef.current = node;

      const mutationObserver = new MutationObserver(checkOverflow);
      const observer = new ResizeObserver(debounce(checkOverflow, 100));

      mutationObserver.observe(node, {
        childList: true,
        attributes: true,
        attributeFilter: ['class', 'style'],
      });
      observer.observe(node);

      return () => {
        observer.disconnect();
        mutationObserver.disconnect();
        valueRef.current = null;
      };
    },
    [checkOverflow]
  );

  const handleKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (
        mode !== 'inline' &&
        (e.key === 'Backspace' || e.key === 'Delete') &&
        selectedValues.size > 0
      ) {
        e.preventDefault();
        removeLastValue();
      }
    },
    [mode, selectedValues.size, removeLastValue]
  );

  if (selectedValues.size === 0 && placeholder) {
    return (
      <span className="min-w-0 overflow-hidden font-normal text-muted-foreground font-mono uppercase">
        {placeholder}
      </span>
    );
  }

  return (
    <div
      {...props}
      ref={handleResize}
      onKeyDown={handleKeyDown}
      className={cn(
        'flex gap-1.5 overflow-hidden',
        mode !== 'inline' && 'w-full',
        shouldWrap && 'h-full flex-wrap',
        className
      )}
    >
      {[...selectedValues]
        .filter((value) => items.has(value))
        .map((value) => (
          <Badge
            variant="outline"
            data-selected-item
            className="group flex items-center gap-1"
            key={value}
            onClick={
              clickToRemove
                ? (e) => {
                    e.stopPropagation();
                    toggleValue(value);
                  }
                : undefined
            }
          >
            {items.get(value)}
            {clickToRemove && (
              <XIcon className="size-2 text-muted-foreground group-hover:text-destructive-foreground" />
            )}
          </Badge>
        ))}
      <div
        ref={overflowRef}
        style={{
          display: overflowAmount > 0 && !shouldWrap ? 'block' : 'none',
        }}
      >
        <Badge variant="outline">+{overflowAmount}</Badge>
      </div>
    </div>
  );
}

export function MultiSelectContent({
  search = true,
  creatable = false,
  children,
  ...props
}: {
  search?: boolean | { placeholder?: string; emptyMessage?: string };
  creatable?: boolean;
  children: ReactNode;
} & Omit<ComponentPropsWithoutRef<typeof Command>, 'children'>) {
  const {
    mode,
    open,
    inputValue,
    setInputValue,
    toggleValue,
    items,
    selectedValues,
  } = useMultiSelectContext();
  const canSearch = typeof search === 'object' ? true : search;

  const inputMatchesItem = [...items.keys()].some(
    (key) => key.toLowerCase() === inputValue.toLowerCase()
  );
  const isAlreadySelected = selectedValues.has(inputValue);
  const showCreatable =
    creatable &&
    inputValue.length > 0 &&
    !inputMatchesItem &&
    !isAlreadySelected;

  const creatableNode = showCreatable && (
    <CommandGroup forceMount>
      <CommandItem
        forceMount
        value={`__create_${inputValue}`}
        onSelect={() => {
          toggleValue(inputValue);
          setInputValue('');
        }}
      >
        <PlusIcon className="mr-2 size-4" />
        Create &ldquo;{inputValue}&rdquo;
      </CommandItem>
    </CommandGroup>
  );

  const emptyMessage =
    typeof search === 'object' ? search.emptyMessage : undefined;

  if (mode === 'inline') {
    return (
      <div
        className={cn(
          'absolute left-0 top-full z-50 mt-1 w-full rounded-md border bg-popover text-popover-foreground shadow-md transition-all',
          open
            ? 'opacity-100 translate-y-0 visible duration-150 ease-out'
            : 'opacity-0 -translate-y-1 invisible duration-0'
        )}
      >
        <CommandList>
          {!showCreatable && <CommandEmpty>{emptyMessage}</CommandEmpty>}
          {children}
          {creatableNode}
        </CommandList>
      </div>
    );
  }

  return (
    <>
      <div style={{ display: 'none' }}>
        <Command>
          <CommandList>{children}</CommandList>
        </Command>
      </div>

      <PopoverContent className="min-w-(--radix-popover-trigger-width) p-0">
        <Command {...props}>
          {canSearch ? (
            <CommandInput
              value={inputValue}
              onValueChange={setInputValue}
              placeholder={
                typeof search === 'object' ? search.placeholder : undefined
              }
            />
          ) : (
            <button autoFocus className="sr-only" />
          )}
          <CommandList>
            {canSearch && !showCreatable && (
              <CommandEmpty>{emptyMessage}</CommandEmpty>
            )}
            {children}
            {creatableNode}
          </CommandList>
        </Command>
      </PopoverContent>
    </>
  );
}

export function MultiSelectItem({
  value,
  children,
  badgeLabel,
  onSelect,
  ...props
}: {
  badgeLabel?: ReactNode;
  value: string;
} & Omit<ComponentPropsWithoutRef<typeof CommandItem>, 'value'>) {
  const { toggleValue, selectedValues, onItemAdded } = useMultiSelectContext();
  const isSelected = selectedValues.has(value);

  useEffect(() => {
    onItemAdded(value, badgeLabel ?? children);
  }, [value, children, onItemAdded, badgeLabel]);

  return (
    <CommandItem
      {...props}
      onSelect={() => {
        toggleValue(value);
        onSelect?.(value);
      }}
      className={props.className}
    >
      <CheckIcon
        className={cn('mr-2 size-4', isSelected ? 'opacity-100' : 'opacity-0')}
      />
      {children}
    </CommandItem>
  );
}

export function MultiSelectGroup(
  props: ComponentPropsWithoutRef<typeof CommandGroup>
) {
  return <CommandGroup {...props} />;
}

export function MultiSelectSeparator(
  props: ComponentPropsWithoutRef<typeof CommandSeparator>
) {
  return <CommandSeparator {...props} />;
}

function useMultiSelectContext() {
  const context = useContext(MultiSelectContext);
  if (context == null) {
    throw new Error(
      'useMultiSelectContext must be used within a MultiSelectContext'
    );
  }

  return context;
}

function debounce<T extends (...args: never[]) => void>(
  func: T,
  wait: number
): (...args: Parameters<T>) => void {
  let timeout: ReturnType<typeof setTimeout> | null = null;

  return function (this: unknown, ...args: Parameters<T>) {
    if (timeout) clearTimeout(timeout);
    timeout = setTimeout(() => func.apply(this, args), wait);
  };
}
