import { Table, type TableProps } from "antd";
import { useTranslation } from "react-i18next";

/** Сколько строк показывать на странице, если таблица длинная. Значение
 * запоминается на пользователя, а не на таблицу: тот, кто предпочитает
 * длинные страницы, предпочитает их везде. */
const PAGE_SIZE_KEY = "nkt-table-page-size";

function readPageSize(): number {
  try {
    const saved = Number(localStorage.getItem(PAGE_SIZE_KEY));
    if ([20, 50, 100].includes(saved)) return saved;
  } catch {
    // Недоступное хранилище — умолчание.
  }
  return 50;
}

/**
 * Таблица с общими настройками.
 *
 * До неё каждая страница повторяла один и тот же набор свойств своими
 * словами: где-то size="small", где-то нет; пагинация то выключена, то
 * своя; пустое состояние — то антовское «No data» по-английски посреди
 * русского интерфейса, то ничего. Здесь это собрано один раз.
 *
 * Порог пагинации не случаен: списки до сотни строк листать неудобнее,
 * чем прокручивать, а после — наоборот.
 */
export function DataTable<T extends object>({
  paginateFrom = 100,
  ...props
}: TableProps<T> & { paginateFrom?: number }) {
  const { t } = useTranslation();
  const rows = props.dataSource?.length ?? 0;

  return (
    // Обёртка .table-wrap остаётся на вызывающей стороне: она уже есть
    // почти везде, а таблицы внутри карточек и модалок иногда должны
    // прокручиваться иначе.
    <Table<T>
      size="small"
      // Липкий заголовок: в длинной таблице колонки иначе теряются
      // ровно тогда, когда становятся нужны.
      sticky
      locale={{ emptyText: t("common.noData") }}
      pagination={
        rows > paginateFrom
          ? {
              defaultPageSize: readPageSize(),
              pageSizeOptions: [20, 50, 100],
              showSizeChanger: true,
              size: "small",
              onShowSizeChange: (_, size) => {
                try {
                  localStorage.setItem(PAGE_SIZE_KEY, String(size));
                } catch {
                  // Не запомнилось — не беда.
                }
              },
            }
          : false
      }
      {...props}
    />
  );
}
