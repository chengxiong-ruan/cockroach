// Copyright 2022 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

import React, { useMemo } from "react";
import classNames from "classnames/bind";
import {
  RecentTransaction,
  RecentTransactionFilters,
} from "src/recentExecutions/types";
import ColumnsSelector, {
  SelectOption,
} from "src/columnsSelector/columnsSelector";
import sortableTableStyles from "src/sortedtable/sortedtable.module.scss";
import { EmptyTransactionsPlaceholder } from "src/transactionsPage/emptyTransactionsPlaceholder";
import { TableStatistics } from "src/tableStatistics";
import {
  ISortedTablePagination,
  SortSetting,
} from "../sortedtable/sortedtable";
import {
  makeRecentTransactionsColumns,
  getColumnOptions,
} from "./recentTransactionsTable";
import { TransactionViewType } from "src/transactionsPage/transactionsPageTypes";
import { calculateActiveFilters } from "src/queryFilter/filter";
import { isSelectedColumn } from "src/columnsSelector/utils";
import { SortedTable } from "src/sortedtable";

const sortableTableCx = classNames.bind(sortableTableStyles);

type RecentTransactionsSectionProps = {
  filters: RecentTransactionFilters;
  isTenant?: boolean;
  pagination: ISortedTablePagination;
  search: string;
  transactions: RecentTransaction[];
  selectedColumns?: string[];
  sortSetting: SortSetting;
  onClearFilters: () => void;
  onChangeSortSetting: (ss: SortSetting) => void;
  onColumnsSelect: (columns: string[]) => void;
};

export const RecentTransactionsSection: React.FC<
  RecentTransactionsSectionProps
> = ({
  filters,
  isTenant,
  pagination,
  search,
  transactions,
  selectedColumns,
  sortSetting,
  onChangeSortSetting,
  onClearFilters,
  onColumnsSelect,
}) => {
  const columns = useMemo(
    () => makeRecentTransactionsColumns(isTenant),
    [isTenant],
  );
  const shownColumns = columns.filter(col =>
    isSelectedColumn(selectedColumns, col),
  );

  const tableColumns: SelectOption[] = getColumnOptions(
    columns,
    selectedColumns,
  );

  const activeFilters = calculateActiveFilters(filters);

  return (
    <section className={sortableTableCx("cl-table-container")}>
      <div>
        <ColumnsSelector
          options={tableColumns}
          onSubmitColumns={onColumnsSelect}
        />
        <TableStatistics
          pagination={pagination}
          search={search}
          totalCount={transactions.length}
          arrayItemName="transactions"
          activeFilters={activeFilters}
          onClearFilters={onClearFilters}
        />
      </div>
      <SortedTable
        data={transactions}
        columns={shownColumns}
        sortSetting={sortSetting}
        onChangeSortSetting={onChangeSortSetting}
        renderNoResult={
          <EmptyTransactionsPlaceholder
            isEmptySearchResults={
              (search?.length > 0 || activeFilters > 0) &&
              transactions.length === 0
            }
            transactionView={TransactionViewType.ACTIVE}
          />
        }
        pagination={pagination}
      />
    </section>
  );
};
