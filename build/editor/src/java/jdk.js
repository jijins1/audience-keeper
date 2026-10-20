// The part of the JDK students use in class, written as Java declarations so
// the same indexer reads it. It feeds completion only: these classes are
// partial, so no "unknown member" warning is ever raised on them.

export const JDK_SOURCE = `
class Object { boolean equals(Object o); int hashCode(); String toString(); Class<?> getClass(); }
final class Class<T> { String getName(); String getSimpleName(); }
interface Comparable<T> { int compareTo(T o); }
interface CharSequence { int length(); char charAt(int index); boolean isEmpty(); String toString(); }
final class String implements CharSequence, Comparable<String> {
  int length(); char charAt(int index); boolean isEmpty(); boolean isBlank();
  String substring(int beginIndex); String substring(int beginIndex, int endIndex);
  int indexOf(String str); int lastIndexOf(String str); boolean contains(CharSequence s);
  boolean startsWith(String prefix); boolean endsWith(String suffix);
  boolean equals(Object o); boolean equalsIgnoreCase(String other); int compareTo(String other);
  String toUpperCase(); String toLowerCase(); String trim(); String strip();
  String replace(CharSequence target, CharSequence replacement); String replaceAll(String regex, String replacement);
  String[] split(String regex); char[] toCharArray(); String repeat(int count); String concat(String str);
  boolean matches(String regex); String formatted(Object... args); Stream<String> lines(); IntStream chars();
  static String valueOf(Object obj); static String format(String format, Object... args);
  static String join(CharSequence delimiter, Iterable<? extends CharSequence> elements);
}
final class StringBuilder implements CharSequence {
  StringBuilder append(Object obj); StringBuilder insert(int offset, Object obj); StringBuilder reverse();
  StringBuilder delete(int start, int end); StringBuilder deleteCharAt(int index);
  StringBuilder replace(int start, int end, String str); void setLength(int newLength);
  int length(); char charAt(int index); int indexOf(String str); String toString();
}
final class Math {
  static final double PI; static final double E;
  static int abs(int a); static double abs(double a); static int max(int a, int b); static double max(double a, double b);
  static int min(int a, int b); static double min(double a, double b); static double pow(double a, double b);
  static double sqrt(double a); static long round(double a); static double floor(double a); static double ceil(double a);
  static double random(); static int floorDiv(int x, int y); static int floorMod(int x, int y);
}
abstract class Number { int intValue(); long longValue(); double doubleValue(); }
final class Integer extends Number implements Comparable<Integer> {
  static final int MAX_VALUE; static final int MIN_VALUE;
  static int parseInt(String s); static Integer valueOf(int i); static String toString(int i);
  static int compare(int x, int y); static int sum(int a, int b); static int max(int a, int b); static int min(int a, int b);
  int compareTo(Integer other);
}
final class Long extends Number implements Comparable<Long> {
  static final long MAX_VALUE; static final long MIN_VALUE; static long parseLong(String s); static Long valueOf(long l);
  int compareTo(Long other);
}
final class Double extends Number implements Comparable<Double> {
  static final double MAX_VALUE; static final double MIN_VALUE; static final double NaN;
  static double parseDouble(String s); static Double valueOf(double d); static int compare(double d1, double d2);
  static boolean isNaN(double v); boolean isNaN(); int compareTo(Double other);
}
final class Boolean implements Comparable<Boolean> { static boolean parseBoolean(String s); static Boolean valueOf(boolean b); boolean booleanValue(); }
final class Character implements Comparable<Character> {
  static boolean isDigit(char ch); static boolean isLetter(char ch); static boolean isLetterOrDigit(char ch);
  static boolean isUpperCase(char ch); static boolean isLowerCase(char ch); static boolean isWhitespace(char ch);
  static char toUpperCase(char ch); static char toLowerCase(char ch); char charValue();
}
final class System {
  static PrintStream out; static PrintStream err; static InputStream in;
  static long currentTimeMillis(); static long nanoTime(); static void exit(int status);
  static String getenv(String name); static String getProperty(String key); static String lineSeparator();
}
class PrintStream { void println(Object x); void println(); void print(Object x); PrintStream printf(String format, Object... args); void flush(); }
class InputStream { int read(); void close(); }

interface Iterable<T> { Iterator<T> iterator(); void forEach(Consumer<? super T> action); }
interface Iterator<E> { boolean hasNext(); E next(); void remove(); }
interface Collection<E> extends Iterable<E> {
  int size(); boolean isEmpty(); boolean contains(Object o); boolean add(E e); boolean remove(Object o);
  boolean addAll(Collection<? extends E> c); boolean removeAll(Collection<?> c); boolean containsAll(Collection<?> c);
  boolean removeIf(Predicate<? super E> filter); void clear(); Stream<E> stream(); Object[] toArray();
}
interface List<E> extends Collection<E> {
  E get(int index); E set(int index, E element); void add(int index, E element); E remove(int index);
  int indexOf(Object o); int lastIndexOf(Object o); List<E> subList(int fromIndex, int toIndex);
  void sort(Comparator<? super E> c); E getFirst(); E getLast(); E removeFirst(); E removeLast(); List<E> reversed();
  static <T> List<T> of(T... elements); static <T> List<T> copyOf(Collection<? extends T> coll);
}
class ArrayList<E> implements List<E> { void ensureCapacity(int minCapacity); void trimToSize(); }
interface Queue<E> extends Collection<E> { boolean offer(E e); E poll(); E peek(); E element(); E remove(); }
interface Deque<E> extends Queue<E> {
  void addFirst(E e); void addLast(E e); E removeFirst(); E removeLast(); E pollFirst(); E pollLast();
  E peekFirst(); E peekLast(); E getFirst(); E getLast(); void push(E e); E pop();
}
class LinkedList<E> implements List<E>, Deque<E> {}
class ArrayDeque<E> implements Deque<E> {}
class PriorityQueue<E> implements Queue<E> { Comparator<? super E> comparator(); }
interface Set<E> extends Collection<E> { static <T> Set<T> of(T... elements); static <T> Set<T> copyOf(Collection<? extends T> coll); }
class HashSet<E> implements Set<E> {}
class LinkedHashSet<E> implements Set<E> {}
class TreeSet<E> implements Set<E> { E first(); E last(); E floor(E e); E ceiling(E e); E pollFirst(); E pollLast(); }
interface Entry<K, V> { K getKey(); V getValue(); V setValue(V value); }
interface Map<K, V> {
  int size(); boolean isEmpty(); V get(Object key); V put(K key, V value); V remove(Object key);
  V getOrDefault(Object key, V defaultValue); V putIfAbsent(K key, V value);
  boolean containsKey(Object key); boolean containsValue(Object value); void putAll(Map<? extends K, ? extends V> m);
  Set<K> keySet(); Collection<V> values(); Set<Entry<K, V>> entrySet(); void clear();
  V merge(K key, V value, BiFunction<? super V, ? super V, ? extends V> remapping);
  V computeIfAbsent(K key, Function<? super K, ? extends V> mapping);
  V compute(K key, BiFunction<? super K, ? super V, ? extends V> remapping);
  void forEach(BiConsumer<? super K, ? super V> action);
  static <K, V> Map<K, V> of(); static <K, V> Map<K, V> copyOf(Map<? extends K, ? extends V> map);
}
class HashMap<K, V> implements Map<K, V> {}
class LinkedHashMap<K, V> implements Map<K, V> {}
class TreeMap<K, V> implements Map<K, V> { K firstKey(); K lastKey(); Entry<K, V> firstEntry(); Entry<K, V> lastEntry(); }

final class Optional<T> {
  boolean isPresent(); boolean isEmpty(); T get(); T orElse(T other); T orElseGet(Supplier<? extends T> supplier); T orElseThrow();
  void ifPresent(Consumer<? super T> action); <U> Optional<U> map(Function<? super T, ? extends U> mapper);
  Optional<T> filter(Predicate<? super T> predicate);
  static <T> Optional<T> of(T value); static <T> Optional<T> ofNullable(T value); static <T> Optional<T> empty();
}
final class OptionalInt { boolean isPresent(); int getAsInt(); int orElse(int other); }
final class OptionalDouble { boolean isPresent(); double getAsDouble(); double orElse(double other); }
interface Stream<T> {
  Stream<T> filter(Predicate<? super T> predicate); <R> Stream<R> map(Function<? super T, ? extends R> mapper);
  IntStream mapToInt(ToIntFunction<? super T> mapper); DoubleStream mapToDouble(ToDoubleFunction<? super T> mapper);
  <R> Stream<R> flatMap(Function<? super T, ? extends Stream<? extends R>> mapper);
  Stream<T> sorted(); Stream<T> sorted(Comparator<? super T> comparator); Stream<T> distinct();
  Stream<T> limit(long maxSize); Stream<T> skip(long n); Stream<T> peek(Consumer<? super T> action);
  void forEach(Consumer<? super T> action); <R, A> R collect(Collector<? super T, A, R> collector); List<T> toList();
  long count(); Optional<T> findFirst(); Optional<T> findAny();
  Optional<T> min(Comparator<? super T> comparator); Optional<T> max(Comparator<? super T> comparator);
  boolean anyMatch(Predicate<? super T> predicate); boolean allMatch(Predicate<? super T> predicate); boolean noneMatch(Predicate<? super T> predicate);
  T reduce(T identity, BinaryOperator<T> accumulator);
  static <T> Stream<T> of(T... values); static <T> Stream<T> empty();
}
interface IntStream {
  int sum(); long count(); OptionalDouble average(); OptionalInt min(); OptionalInt max(); int[] toArray();
  IntStream filter(IntPredicate predicate); IntStream map(IntUnaryOperator mapper); Stream<Integer> boxed();
  void forEach(IntConsumer action); static IntStream range(int startInclusive, int endExclusive);
  static IntStream rangeClosed(int startInclusive, int endInclusive); static IntStream of(int... values);
}
interface DoubleStream { double sum(); long count(); OptionalDouble average(); OptionalDouble min(); OptionalDouble max(); double[] toArray(); Stream<Double> boxed(); }
final class Collectors {
  static <T> Collector<T, ?, List<T>> toList(); static <T> Collector<T, ?, Set<T>> toSet();
  static Collector<CharSequence, ?, String> joining(); static Collector<CharSequence, ?, String> joining(CharSequence delimiter);
  static <T, K> Collector<T, ?, Map<K, List<T>>> groupingBy(Function<? super T, ? extends K> classifier);
  static <T> Collector<T, ?, Long> counting();
  static <T, K, U> Collector<T, ?, Map<K, U>> toMap(Function<? super T, ? extends K> keyMapper, Function<? super T, ? extends U> valueMapper);
  static <T> Collector<T, ?, Integer> summingInt(ToIntFunction<? super T> mapper);
  static <T> Collector<T, ?, Double> averagingDouble(ToDoubleFunction<? super T> mapper);
}
interface Comparator<T> {
  int compare(T o1, T o2); Comparator<T> reversed(); Comparator<T> thenComparing(Comparator<? super T> other);
  static <T, U> Comparator<T> comparing(Function<? super T, ? extends U> keyExtractor);
  static <T> Comparator<T> comparingInt(ToIntFunction<? super T> keyExtractor);
  static <T> Comparator<T> comparingDouble(ToDoubleFunction<? super T> keyExtractor);
  static <T> Comparator<T> naturalOrder(); static <T> Comparator<T> reverseOrder();
}
final class Arrays {
  static String toString(Object[] a); static void sort(Object[] a); static <T> List<T> asList(T... a);
  static <T> Stream<T> stream(T[] array); static void fill(Object[] a, Object val); static boolean equals(Object[] a, Object[] b);
  static int[] copyOf(int[] original, int newLength); static int binarySearch(int[] a, int key);
}
final class Collections {
  static void sort(List<?> list); static void reverse(List<?> list); static void shuffle(List<?> list);
  static <T> T max(Collection<? extends T> coll); static <T> T min(Collection<? extends T> coll);
  static int frequency(Collection<?> c, Object o); static <T> List<T> emptyList(); static <T> List<T> unmodifiableList(List<? extends T> list);
}
final class Objects {
  static boolean equals(Object a, Object b); static int hash(Object... values); static String toString(Object o);
  static <T> T requireNonNull(T obj); static <T> T requireNonNull(T obj, String message);
  static boolean isNull(Object obj); static boolean nonNull(Object obj);
}
interface Runnable { void run(); }
interface Callable<V> { V call(); }
interface Function<T, R> { R apply(T t); }
interface BiFunction<T, U, R> { R apply(T t, U u); }
interface UnaryOperator<T> extends Function<T, T> {}
interface BinaryOperator<T> extends BiFunction<T, T, T> {}
interface Consumer<T> { void accept(T t); }
interface BiConsumer<T, U> { void accept(T t, U u); }
interface Supplier<T> { T get(); }
interface Predicate<T> { boolean test(T t); Predicate<T> negate(); Predicate<T> and(Predicate<? super T> other); Predicate<T> or(Predicate<? super T> other); }
abstract class Enum<E> implements Comparable<E> { String name(); int ordinal(); int compareTo(E o); }
class Throwable { String getMessage(); Throwable getCause(); void printStackTrace(); StackTraceElement[] getStackTrace(); }
class Exception extends Throwable {}
class RuntimeException extends Exception {}
class Error extends Throwable {}
class IllegalArgumentException extends RuntimeException {}
class IllegalStateException extends RuntimeException {}
class NullPointerException extends RuntimeException {}
class ArithmeticException extends RuntimeException {}
class IndexOutOfBoundsException extends RuntimeException {}
class UnsupportedOperationException extends RuntimeException {}
class NumberFormatException extends IllegalArgumentException {}
class IOException extends Exception {}
class InterruptedException extends Exception {}
final class Scanner {
  String nextLine(); String next(); int nextInt(); double nextDouble(); long nextLong(); boolean nextBoolean();
  boolean hasNext(); boolean hasNextLine(); boolean hasNextInt(); void close();
}
class Random { int nextInt(); int nextInt(int bound); double nextDouble(); boolean nextBoolean(); long nextLong(); }
final class LocalDate implements Comparable<LocalDate> {
  static LocalDate now(); static LocalDate of(int year, int month, int dayOfMonth); static LocalDate parse(CharSequence text);
  int getYear(); int getMonthValue(); int getDayOfMonth(); DayOfWeek getDayOfWeek();
  LocalDate plusDays(long days); LocalDate minusDays(long days); LocalDate plusMonths(long months); LocalDate plusYears(long years);
  boolean isBefore(LocalDate other); boolean isAfter(LocalDate other); boolean isLeapYear(); int compareTo(LocalDate other);
}
final class LocalDateTime { static LocalDateTime now(); LocalDate toLocalDate(); int getHour(); int getMinute(); LocalDateTime plusHours(long hours); boolean isBefore(LocalDateTime other); boolean isAfter(LocalDateTime other); }
// JUnit 5 and Mockito: the course's tests use them.
final class Assertions {
  static void assertEquals(Object expected, Object actual); static void assertEquals(Object expected, Object actual, String message);
  static void assertNotEquals(Object unexpected, Object actual); static void assertTrue(boolean condition); static void assertFalse(boolean condition);
  static void assertNull(Object actual); static void assertNotNull(Object actual); static void assertSame(Object expected, Object actual);
  static <T> T assertThrows(Class<T> expectedType, Executable executable); static void assertAll(Executable... executables); static void fail(String message);
}
interface Executable { void execute(); }
final class Mockito {
  static <T> OngoingStubbing<T> when(T methodCall); static <T> T verify(T mock); static <T> T verify(T mock, VerificationMode mode);
  static <T> T mock(Class<T> classToMock); static <T> T spy(T object); static void verifyNoInteractions(Object... mocks);
  static void verifyNoMoreInteractions(Object... mocks); static VerificationMode times(int wantedNumberOfInvocations);
  static VerificationMode never(); static VerificationMode atLeastOnce(); static <T> T any(); static <T> T any(Class<T> type);
  static <T> T eq(T value); static int anyInt(); static long anyLong(); static String anyString(); static Stubber doThrow(Throwable... toBeThrown);
  static Stubber doNothing(); static Stubber doReturn(Object toBeReturned);
}
interface OngoingStubbing<T> {
  OngoingStubbing<T> thenReturn(T value); OngoingStubbing<T> thenReturn(T value, T... values);
  OngoingStubbing<T> thenThrow(Throwable... throwables); OngoingStubbing<T> thenAnswer(Answer<?> answer);
}
interface Stubber { <T> T when(T mock); }
interface VerificationMode {}
interface Answer<T> {}
class Thread { static void sleep(long millis); static Thread currentThread(); void start(); void join(); void interrupt(); String getName(); }
`;

// Known JDK names without detailed members: no "unknown type" warning for them.
export const JDK_NAMES = new Set(`
Override Deprecated FunctionalInterface SuppressWarnings SafeVarargs Void Byte Short Float Number Record Iterable
StackTraceElement Collector IntPredicate IntUnaryOperator IntConsumer IntFunction ToIntFunction ToDoubleFunction ToLongFunction
BigDecimal BigInteger UUID Instant Duration Period DayOfWeek Month LocalTime ZonedDateTime DateTimeFormatter
File Path Paths Files BufferedReader InputStreamReader FileReader FileWriter BufferedWriter PrintWriter Reader Writer
OutputStream FileInputStream FileOutputStream Serializable Closeable AutoCloseable Cloneable Pattern Matcher
Executors ExecutorService Future CompletableFuture TimeUnit AtomicInteger AtomicLong ConcurrentHashMap CopyOnWriteArrayList
ReentrantLock Lock Stack Vector Hashtable EnumMap EnumSet SortedMap SortedSet NavigableMap NavigableSet ListIterator
Iterator Spliterator Properties Locale Currency NumberFormat DecimalFormat SimpleDateFormat Date Calendar Timer
ClassCastException ArrayIndexOutOfBoundsException StringIndexOutOfBoundsException NoSuchElementException
ConcurrentModificationException CloneNotSupportedException FileNotFoundException UncheckedIOException
StackOverflowError OutOfMemoryError AssertionError
ArgumentCaptor MockitoExtension ExtendWith SpringBootTest WebMvcTest DataJpaTest MockMvc MockBean Test BeforeEach AfterEach
ResponseEntity HttpStatus RabbitTemplate Message Queue Logger LoggerFactory
`.split(/\s+/).filter(Boolean));
