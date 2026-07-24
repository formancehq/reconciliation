"use client"

import { useCallback } from "react"

import { ValueCombobox } from "@/components/explorer-v3/ValueCombobox"

export function LedgerCombobox({
  value,
  onChange,
  options,
  placeholder = "Select or enter a ledger",
  id,
}: {
  value: string
  onChange: (value: string) => void
  options: string[]
  placeholder?: string
  id?: string
}) {
  const fetchValues = useCallback(
    async (search: string) => {
      const needle = search.toLocaleLowerCase()
      const values = options.filter(
        (name) => !needle || name.toLocaleLowerCase().includes(needle)
      )
      return { values, capped: false }
    },
    [options]
  )

  return (
    <ValueCombobox
      id={id}
      ariaLabel="Ledger"
      value={value}
      onChange={onChange}
      placeholder={placeholder}
      showAllOnOpen
      modal
      wrapperClassName="w-full"
      className="w-full font-mono text-sm"
      fetchValues={fetchValues}
    />
  )
}
