import { ArrowLeft, Search } from "lucide-react";
import { Link, useLocation } from "react-router";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/status-badge";
import { serviceStatusVariant } from "@/lib/badge-variants";

interface Props {
  name: string;
  status: string;
  symptom?: string;
  onInvestigate: () => void;
}

export function ServiceHeader({ name, status, symptom, onInvestigate }: Props) {
  const { search } = useLocation();
  return (
    <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
      <div className="flex items-center gap-3 min-w-0 flex-wrap">
        <Link
          to={`/${search}`}
          className="text-xs text-muted-foreground hover:text-foreground transition-colors mono flex items-center gap-1"
        >
          <ArrowLeft className="h-3 w-3" />
          Home
        </Link>
        <span className="font-heading text-xl font-bold text-foreground truncate">
          {name}
        </span>
        <StatusBadge variant={serviceStatusVariant(status)}>
          {status}
        </StatusBadge>
        {symptom && (
          <span className="text-[11px] text-muted-foreground mono">
            {symptom.replace(/_/g, " ")}
          </span>
        )}
      </div>
      <Button
        type="button"
        size="sm"
        onClick={onInvestigate}
        className="shrink-0 text-xs"
      >
        <Search className="size-3" />
        Investigate
      </Button>
    </div>
  );
}
