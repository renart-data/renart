"use client";

import { LoaderCircle } from "lucide-react";
import type { ComponentType } from "react";

import { Badge } from "@/components/ui/badge";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

import { ConnectionTypeIcon, friendlyConnectionType } from "./connection-type-icon";

export type ConnectionSelectOption = {
  value: string;
  label: string;
  connectionType?: string;
  detail?: string;
  badge?: string;
  badgeVariant?: "default" | "secondary" | "destructive" | "outline" | "muted";
  disabled?: boolean;
  icon?: ComponentType;
};

export type ConnectionSelectGroup = {
  label?: string;
  options: ConnectionSelectOption[];
};

export function ConnectionSelect({
  value,
  groups,
  placeholder = "Choose a connection",
  disabled = false,
  id,
  ariaLabel,
  size = "default",
  className,
  contentAlign = "center",
  loading = false,
  onValueChange,
}: {
  value?: string;
  groups: ConnectionSelectGroup[];
  placeholder?: string;
  disabled?: boolean;
  id?: string;
  ariaLabel?: string;
  size?: "sm" | "default";
  className?: string;
  contentAlign?: "start" | "center" | "end";
  loading?: boolean;
  onValueChange: (value: string) => void;
}) {
  const selected = groups
    .flatMap((group) => group.options)
    .find((option) => option.value === value);
  return (
    <Select value={value} disabled={disabled} onValueChange={onValueChange}>
      <SelectTrigger
        id={id}
        size={size}
        aria-label={ariaLabel}
        className={cn(
          "min-w-0 overflow-hidden [&_[data-slot=select-value]]:min-w-0 [&_[data-slot=select-value]]:overflow-hidden",
          className,
        )}
      >
        <SelectValue placeholder={placeholder}>
          {selected ? <ConnectionSelectValue option={selected} loading={loading} /> : null}
        </SelectValue>
      </SelectTrigger>
      <SelectContent align={contentAlign}>
        {groups.map((group, index) => {
          if (group.options.length === 0) return null;
          return (
            <ConnectionSelectGroupContent
              key={`${group.label ?? "connections"}:${index}`}
              group={group}
              separator={index > 0}
            />
          );
        })}
      </SelectContent>
    </Select>
  );
}

function ConnectionSelectGroupContent({
  group,
  separator,
}: {
  group: ConnectionSelectGroup;
  separator: boolean;
}) {
  return (
    <>
      {separator ? <SelectSeparator /> : null}
      <SelectGroup>
        {group.label ? <SelectLabel>{group.label}</SelectLabel> : null}
        {group.options.map((option) => (
          <SelectItem
            key={option.value}
            value={option.value}
            disabled={option.disabled}
            aria-label={option.label}
          >
            <ConnectionSelectOptionContent option={option} />
          </SelectItem>
        ))}
      </SelectGroup>
    </>
  );
}

function ConnectionSelectValue({
  option,
  loading,
}: {
  option: ConnectionSelectOption;
  loading: boolean;
}) {
  const Icon = option.icon;
  return (
    <span className="flex min-w-0 items-center gap-1.5">
      {loading ? (
        <LoaderCircle className="size-4 shrink-0 animate-spin text-muted-foreground" />
      ) : option.connectionType ? (
        <ConnectionTypeIcon connectionType={option.connectionType} className="size-5" />
      ) : Icon ? (
        <Icon />
      ) : null}
      <span className="truncate">{option.label}</span>
    </span>
  );
}

export function ConnectionSelectOptionContent({ option }: { option: ConnectionSelectOption }) {
  const Icon = option.icon;
  return (
    <span className="flex min-w-0 items-center gap-2 pr-5">
      {option.connectionType ? (
        <ConnectionTypeIcon connectionType={option.connectionType} />
      ) : Icon ? (
        <Icon />
      ) : null}
      <span className="min-w-0 flex-1">
        <span className="block truncate">{option.label}</span>
        {option.detail || option.connectionType ? (
          <span aria-hidden="true" className="block truncate text-[10px] text-muted-foreground">
            {option.detail || friendlyConnectionType(option.connectionType)}
          </span>
        ) : null}
      </span>
      {option.badge ? (
        <Badge variant={option.badgeVariant ?? "outline"} size="xs" aria-hidden="true">
          {option.badge}
        </Badge>
      ) : null}
    </span>
  );
}
